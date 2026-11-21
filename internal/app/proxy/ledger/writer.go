// Package ledger commits proxy request and money-history row pairs together.
package ledger

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"sync/atomic"
	"time"
)

// Pair holds the indexed keys and already serialized evidence. The writer
// neither reinterprets the JSON nor retains a pointer to the caller's record.
type Pair struct {
	ID, CallerID, Route, Method, Path, Outcome string
	Started                                    int64
	Data, Money                                string
}

type Writer struct {
	db                               *sql.DB
	startStmt, finishStmt, moneyStmt *sql.Stmt
	queue                            chan *operation
	stop, stopped                    chan struct{}
}

// New starts one writer. The caller owns the connection pool and statements.
func New(db *sql.DB, start, finish, money *sql.Stmt) *Writer {
	s := &Writer{db: db, startStmt: start, finishStmt: finish, moneyStmt: money, queue: make(chan *operation, 16), stop: make(chan struct{}), stopped: make(chan struct{})}
	go s.run()
	return s
}

// Stop rejects new submissions. Call once before closing the pool, then Wait.
func (s *Writer) Stop() { close(s.stop) }
func (s *Writer) Wait() { <-s.stopped }
func (s *Writer) Start(ctx context.Context, p Pair) (int64, error) {
	seq, err := s.submit(ctx, &operation{pair: p, start: true})
	if err != nil {
		return 0, err
	}
	return seq, nil
}
func (s *Writer) Finish(ctx context.Context, p Pair) error {
	_, err := s.submit(ctx, &operation{pair: p})
	return err
}

type operation struct {
	ctx     context.Context
	pair    Pair
	start   bool
	claimed atomic.Bool
	done    chan struct{}
	seq     int64
	err     error
}

// Only this queue is shared between starts and finishes. Its writer never
// sleeps to form a batch or reserves a database connection between batches.
func (s *Writer) submit(ctx context.Context, w *operation) (int64, error) {
	w.ctx, w.done = ctx, make(chan struct{})
	select {
	case <-s.stop:
		return 0, sql.ErrConnDone
	default:
	}
	select {
	case s.queue <- w:
	case <-s.stop:
		return 0, sql.ErrConnDone
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	select {
	case <-w.done:
		return w.seq, w.err
	case <-ctx.Done():
		// A canceled but unclaimed entry is inert. Once claimed, wait for
		// the definitive commit/rollback result, even during shutdown.
		if w.claimed.CompareAndSwap(false, true) {
			return 0, ctx.Err()
		}
		<-w.done
		return w.seq, w.err
	case <-s.stopped:
		select {
		case <-w.done:
			return w.seq, w.err
		default:
			return 0, sql.ErrConnDone
		}
	}
}

func (s *Writer) run() {
	defer close(s.stopped)
	for {
		select {
		case <-s.stop:
			return
		case first := <-s.queue:
			if !first.claimed.CompareAndSwap(false, true) {
				continue
			}
			conn, err := s.db.Conn(first.ctx)
			if err != nil {
				first.err = err
				close(first.done)
				continue
			}
			// Snapshot only operations already waiting at connection acquisition.
			batch := []*operation{first}
			for range len(s.queue) {
				w := <-s.queue
				if w.claimed.CompareAndSwap(false, true) {
					batch = append(batch, w)
				}
			}
			s.commit(conn, batch)
			conn.Close()
		}
	}
}

func (s *Writer) commit(conn *sql.Conn, batch []*operation) {
	failed, err := s.tryCommit(conn, batch)
	if failed >= 0 && len(batch) > 1 {
		// A statement failed and rollback succeeded. Run unaffected requests in
		// submission order, in their own transactions, without replaying a COMMIT.
		for i, w := range batch {
			if i != failed {
				_, w.err = s.tryCommit(conn, batch[i:i+1])
			} else {
				w.err = err
			}
			close(w.done)
		}
		return
	}
	for _, w := range batch {
		w.err = err
		close(w.done)
	}
}

func (s *Writer) tryCommit(conn *sql.Conn, batch []*operation) (int, error) {
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		return -1, err
	}
	for i, w := range batch {
		if err = w.apply(tx, s); err != nil {
			return rollbackLedger(conn, tx, i, err)
		}
	}
	for i, w := range batch {
		if err = w.ctx.Err(); err != nil {
			return rollbackLedger(conn, tx, i, err)
		}
	}
	if err = tx.Commit(); err != nil {
		// SQLite may leave the transaction open after a failed deferred constraint.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, e := conn.ExecContext(ctx, "ROLLBACK"); e != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}
	return -1, err
}

func rollbackLedger(conn *sql.Conn, tx *sql.Tx, i int, cause error) (int, error) {
	if err := tx.Rollback(); err != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		return -1, fmt.Errorf("ledger rollback after %v: %w", cause, err)
	}
	return i, cause
}

func (w *operation) apply(tx *sql.Tx, s *Writer) error {
	rec, ctx := &w.pair, w.ctx
	if w.start {
		result, err := tx.StmtContext(ctx, s.startStmt).ExecContext(ctx, rec.ID, rec.Started, rec.CallerID, rec.Route, rec.Method, rec.Path, "unknown", rec.Data)
		if err != nil {
			return err
		}
		w.seq, err = result.LastInsertId()
		if err != nil {
			return err
		}
	} else {
		result, err := tx.StmtContext(ctx, s.finishStmt).ExecContext(ctx, rec.Outcome, rec.Data, rec.ID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("terminal request row is missing")
		}
	}
	_, err := tx.StmtContext(ctx, s.moneyStmt).ExecContext(ctx, rec.ID, rec.Started, rec.CallerID, rec.Route, rec.Money)
	return err
}
