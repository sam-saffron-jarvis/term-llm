package ledger

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

type record struct {
	Pair
	Seq int64
}
type fixture struct {
	*Writer
	closed bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE requests(seq INTEGER PRIMARY KEY,id TEXT UNIQUE,started,caller,route,method,path,outcome,data);CREATE TABLE money_history(request_id TEXT PRIMARY KEY,started,caller,route,data);`)
	if err != nil {
		t.Fatal(err)
	}
	prep := func(q string) *sql.Stmt {
		st, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	s := &fixture{Writer: New(db, prep("INSERT INTO requests(id,started,caller,route,method,path,outcome,data) VALUES(?,?,?,?,?,?,?,?)"), prep("UPDATE requests SET outcome=?,data=? WHERE id=?"), prep("INSERT INTO money_history VALUES(?,?,?,?,?) ON CONFLICT(request_id) DO UPDATE SET data=excluded.data"))}
	t.Cleanup(func() { s.close() })
	return s
}
func (s *fixture) start(ctx context.Context, r *record) error {
	seq, err := s.Start(ctx, r.Pair)
	if err == nil {
		r.Seq = seq
	}
	return err
}
func (s *fixture) close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.Stop()
	err := s.db.Close()
	s.Wait()
	return err
}
func execSQL(t *testing.T, s *fixture, q string) {
	t.Helper()
	if _, err := s.db.Exec(q); err != nil {
		t.Fatal(err)
	}
}
func count(t *testing.T, s *fixture, q string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Hold the single write connection to force a real backlog rather than relying
// on scheduler timing to put unrelated operations in one transaction.
func ledgerBacklog(t *testing.T, s *fixture) func() {
	t.Helper()
	conn, err := s.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return func() { conn.Close() }
}
func waitLedger(t *testing.T, ready func() bool) {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for !ready() {
		if time.Now().After(until) {
			t.Fatal("timed out awaiting ledger backlog")
		}
		runtime.Gosched()
	}
}
func submitAsync(write func() error) <-chan error {
	done := make(chan error, 1)
	go func() { done <- write() }()
	return done
}

func TestLedgerMixedBatchRejectionIsolation(t *testing.T) {
	s := newFixture(t)
	ctx := t.Context()
	prior := record{Pair: Pair{ID: "prior"}}
	if err := s.start(ctx, &prior); err != nil {
		t.Fatal(err)
	}
	prior.Outcome = "complete"
	execSQL(t, s, `CREATE TRIGGER reject_money BEFORE INSERT ON money_history WHEN NEW.request_id='reject' BEGIN SELECT RAISE(FAIL,'reject'); END`)
	release := ledgerBacklog(t, s)
	waits := s.db.Stats().WaitCount
	finish := submitAsync(func() error { return s.Finish(ctx, prior.Pair) })
	waitLedger(t, func() bool { return s.db.Stats().WaitCount > waits })
	bad, good, canceled := record{Pair: Pair{ID: "reject"}}, record{Pair: Pair{ID: "good"}}, record{Pair: Pair{ID: "canceled"}}
	reject := submitAsync(func() error { return s.start(ctx, &bad) })
	waitLedger(t, func() bool { return len(s.queue) == 1 })
	accept := submitAsync(func() error { return s.start(ctx, &good) })
	waitLedger(t, func() bool { return len(s.queue) == 2 })
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	skipped := submitAsync(func() error { return s.start(cancelCtx, &canceled) })
	waitLedger(t, func() bool { return len(s.queue) == 3 })
	cancel()
	if err := <-skipped; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-finish:
		t.Fatal("finish acknowledged before write connection released")
	case <-accept:
		t.Fatal("start acknowledged before write connection released")
	default:
	}
	release()
	if err := <-finish; err != nil {
		t.Fatal(err)
	}
	if err := <-reject; err == nil {
		t.Fatal("money rejection ignored")
	}
	if err := <-accept; err != nil {
		t.Fatal(err)
	}
	if bad.Seq != 0 || canceled.Seq != 0 || good.Seq <= prior.Seq {
		t.Fatal(bad.Seq, canceled.Seq, good.Seq)
	}
	if count(t, s, "SELECT count(*) FROM requests WHERE id='prior' AND outcome='complete'") != 1 || count(t, s, "SELECT count(*) FROM requests JOIN money_history ON requests.id=money_history.request_id") != 2 || count(t, s, "SELECT count(*) FROM requests") != 2 || count(t, s, "SELECT count(*) FROM money_history") != 2 {
		t.Fatal("incorrect ledger rows after rejection")
	}
}

func TestLedgerCommitFailureIsNotAcknowledged(t *testing.T) {
	s := newFixture(t)
	ctx := t.Context()
	execSQL(t, s, `PRAGMA foreign_keys=ON; CREATE TABLE ledger_parent(id INTEGER PRIMARY KEY); CREATE TABLE ledger_child(id INTEGER REFERENCES ledger_parent(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER defer_error AFTER INSERT ON money_history WHEN NEW.request_id='bad' BEGIN INSERT INTO ledger_child VALUES(42); END`)
	release := ledgerBacklog(t, s)
	waits := s.db.Stats().WaitCount
	good, bad := record{Pair: Pair{ID: "good"}}, record{Pair: Pair{ID: "bad"}}
	first := submitAsync(func() error { return s.start(ctx, &good) })
	waitLedger(t, func() bool { return s.db.Stats().WaitCount > waits })
	second := submitAsync(func() error { return s.start(ctx, &bad) })
	waitLedger(t, func() bool { return len(s.queue) == 1 })
	release()
	for _, done := range []<-chan error{first, second} {
		if <-done == nil {
			t.Fatal("acknowledged failed COMMIT")
		}
	}
	if good.Seq != 0 || bad.Seq != 0 || count(t, s, "SELECT count(*) FROM requests") != 0 || count(t, s, "SELECT count(*) FROM money_history") != 0 {
		t.Fatal("failed COMMIT leaked rows or sequence")
	}
	if err := s.start(ctx, &good); err != nil {
		t.Fatalf("writer unusable after failed COMMIT: %v", err)
	}
}

func TestLedgerCloseUnblocksFullQueue(t *testing.T) {
	s := newFixture(t)
	_ = ledgerBacklog(t, s)
	waits := s.db.Stats().WaitCount
	start := func() error { return s.start(t.Context(), &record{Pair: Pair{ID: "closed"}}) }
	first := submitAsync(start)
	waitLedger(t, func() bool { return s.db.Stats().WaitCount > waits })
	done := []<-chan error{first}
	for range cap(s.queue) + 1 {
		done = append(done, submitAsync(start))
	}
	waitLedger(t, func() bool { return len(s.queue) == cap(s.queue) })
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	for _, ch := range done {
		if <-ch == nil {
			t.Fatal("acknowledged closed write")
		}
	}
	if err := start(); err == nil {
		t.Fatal("accepted write after close")
	}
}

// Exercise cancellation after all three operations have been selected, not
// just cancellation of a still-queued submission. The first provisional
// insert must roll back and both live neighbours must ultimately commit.
func TestLedgerClaimedMiddleCanceled(t *testing.T) {
	s := newFixture(t)
	ctx := t.Context()
	middle, cancel := context.WithCancel(ctx)
	cancel()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	batch := []*operation{{ctx: ctx, pair: Pair{ID: "first"}}, {ctx: middle, pair: Pair{ID: "middle"}}, {ctx: ctx, pair: Pair{ID: "last"}}}
	for _, w := range batch {
		w.start = true
		w.done = make(chan struct{})
		w.claimed.Store(true)
	}
	s.commit(conn, batch)
	conn.Close()
	if batch[0].err != nil || !errors.Is(batch[1].err, context.Canceled) || batch[2].err != nil {
		t.Fatal(batch[0].err, batch[1].err, batch[2].err)
	}
	if batch[0].seq <= 0 || batch[2].seq <= batch[0].seq || count(t, s, "SELECT count(*) FROM requests JOIN money_history ON request_id=id WHERE id IN ('first','last')") != 2 || count(t, s, "SELECT count(*) FROM requests WHERE id='middle'") != 0 || count(t, s, "SELECT count(*) FROM money_history WHERE request_id='middle'") != 0 {
		t.Fatal("canceled middle operation corrupted batch")
	}
}
