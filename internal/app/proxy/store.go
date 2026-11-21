package proxy

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/term-llm/term-llm/internal/fileprivacy"
	"github.com/term-llm/term-llm/internal/sqliteutil"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE configuration (id INTEGER PRIMARY KEY CHECK(id=1), revision INTEGER NOT NULL, data TEXT NOT NULL);
INSERT INTO configuration VALUES(1,0,'` + emptyProviderConfig + `');
CREATE TABLE secrets (name TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE callers (id TEXT PRIMARY KEY, name TEXT NOT NULL, hash TEXT UNIQUE NOT NULL, created INTEGER NOT NULL, revoked INTEGER NOT NULL DEFAULT 0, created_by TEXT NOT NULL DEFAULT '', external_id TEXT NOT NULL DEFAULT '');
CREATE TABLE grants (caller TEXT NOT NULL, route TEXT NOT NULL, authority TEXT NOT NULL, PRIMARY KEY(caller,route));
CREATE TABLE requests (seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT UNIQUE NOT NULL, started INTEGER NOT NULL, caller TEXT NOT NULL, route TEXT NOT NULL, method TEXT NOT NULL, path TEXT NOT NULL, outcome TEXT NOT NULL, data TEXT NOT NULL);
CREATE INDEX requests_time ON requests(started,seq);
CREATE INDEX requests_caller ON requests(caller,seq);
CREATE INDEX requests_route ON requests(route,seq);
` + accountSchema + moneySchema + adminKeySchemaV6 + claudeSharedSchema + `
PRAGMA user_version=9;`

func openStore(dir string, fileConfig ...*Config) (*store, error) {
	if err := fileprivacy.MkdirAll(dir); err != nil {
		return nil, fmt.Errorf("create private proxy state: %w", err)
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() || fileprivacy.CheckDir(st) != nil {
		return nil, fmt.Errorf("state directory must be private (0700)")
	}
	path := filepath.Join(dir, "proxy.db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open proxy database: %w", err)
	}
	f.Close()
	st, err = os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || fileprivacy.CheckFile(st) != nil {
		return nil, fmt.Errorf("database must be a private regular file")
	}
	dsn := sqliteutil.FileURI(path) + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(3000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open proxy store: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &store{db: db}
	if err = s.initialize(fileConfig...); err != nil {
		return nil, errors.Join(fmt.Errorf("initialize proxy store: %w", err), s.close())
	}
	// WAL readers can observe the latest committed grants and prices without
	// queuing behind a ledger write. They cannot change custody PRAGMAs or write.
	s.readDB, err = sql.Open("sqlite", sqliteutil.FileURI(path)+"?mode=ro&_pragma=query_only(ON)&_pragma=busy_timeout(3000)")
	if err != nil {
		return nil, errors.Join(err, s.close())
	}
	s.readDB.SetMaxOpenConns(2)
	if err = s.prepareStatements(); err != nil {
		return nil, err
	}
	return s, nil
}
func randomToken(prefix string) string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b[:])
}
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func (s *store) load() (Config, int64, map[string]material, error) {
	var data string
	var revision int64
	var c Config
	if err := s.db.QueryRow("SELECT revision,data FROM configuration WHERE id=1").Scan(&revision, &data); err != nil {
		return c, 0, nil, err
	}
	if err := json.Unmarshal([]byte(data), &c); err != nil {
		return c, 0, nil, err
	}
	secrets, err := s.materials()
	return c, revision, secrets, err
}
func (s *store) materials() (map[string]material, error) {
	rows, err := s.db.Query("SELECT name,value FROM secrets")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]material{}
	for rows.Next() {
		var name string
		var m material
		if err := rows.Scan(&name, &m.Value); err != nil {
			return nil, err
		}
		out[name] = m
	}
	return out, rows.Err()
}
func reconcileGrants(tx *sql.Tx, snap *snapshot) (int64, error) {
	// Rewrite only known DNS-independent approvals before removing stale grants.
	for _, r := range snap.routes {
		if r.legacySRVAuthority != "" {
			if _, err := tx.Exec("UPDATE grants SET authority=? WHERE route=? AND authority=?", r.authority, r.Name, r.legacySRVAuthority); err != nil {
				return 0, fmt.Errorf("upgrade SRV grant: %w", err)
			}
		}
	}
	rows, err := tx.Query("SELECT DISTINCT route,authority FROM grants")
	if err != nil {
		return 0, err
	}
	type pair struct{ name, authority string }
	var stale []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.name, &p.authority); err != nil {
			rows.Close()
			return 0, err
		}
		found := false
		for _, r := range snap.routes {
			found = found || r.Name == p.name && r.authority == p.authority
		}
		if !found {
			stale = append(stale, p)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	var dropped int64
	for _, p := range stale {
		result, err := tx.Exec("DELETE FROM grants WHERE route=? AND authority=?", p.name, p.authority)
		if err != nil {
			return 0, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		if _, err := tx.Exec("DELETE FROM claude_shared_enrollment WHERE route=? AND authority=?", p.name, p.authority); err != nil {
			return 0, err
		}

		dropped += n
	}
	return dropped, nil
}
func (s *store) saveConfig(snap *snapshot, fileMode bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	dropped, err := reconcileGrants(tx, snap)
	if err != nil {
		return err
	}
	if fileMode {
		_, err = tx.Exec("UPDATE configuration SET revision=? WHERE id=1", snap.revision)
	} else {
		b, e := json.Marshal(snap.config)
		if e != nil {
			return e
		}
		_, err = tx.Exec("UPDATE configuration SET revision=?,data=? WHERE id=1", snap.revision, string(b))
	}
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if dropped > 0 {
		log.Printf("proxy: activation removed %d incompatible route grants; regrant explicitly in Clients", dropped)
	}
	return nil
}

type Caller struct {
	Tag        string   `json:"-"`
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Created    int64    `json:"created"`
	LastUsed   int64    `json:"last_used"`
	Revoked    bool     `json:"revoked"`
	Grants     []string `json:"grants"`
	CreatedBy  string   `json:"created_by"`
	ExternalID string   `json:"external_id"`
}

// The latest admission is an indexed tail lookup, not an aggregate over retained history.
const callerSelect = "SELECT c.id,c.name,c.created,c.revoked,COALESCE((SELECT r.started FROM requests r WHERE r.caller=c.id ORDER BY r.seq DESC LIMIT 1),0),c.created_by,c.external_id FROM callers c"
const callerOrder = " ORDER BY c.created,c.id"
const callersQuery = callerSelect + callerOrder

func (s *store) callers() ([]Caller, error) {
	return listCallers(context.Background(), s.db, "")
}

func (s *store) createCaller(name string, grants []string, snap *snapshot) (Caller, string, error) {
	return s.insertCaller(context.Background(), name, grants, snap, "", "")
}

type Record struct {
	Usage                 *ObservedUsage `json:"usage,omitempty"`
	Money                 *Money         `json:"money,omitempty"`
	Provider              string         `json:"provider,omitempty"`
	App                   string         `json:"app,omitempty"`
	ID                    string         `json:"id"`
	Seq                   int64          `json:"seq"`
	Started               int64          `json:"started"`
	Ended                 int64          `json:"ended"`
	CallerID              string         `json:"caller_id"`
	CallerName            string         `json:"caller_name"`
	Tag                   string         `json:"tag,omitempty"`
	Route                 string         `json:"route"`
	Revision              int64          `json:"revision"`
	Method                string         `json:"method"`
	Path                  string         `json:"path"`
	LocalPrefix           string         `json:"local_prefix"`
	Peer                  string         `json:"peer"`
	Status                int            `json:"status"`
	Outcome               string         `json:"outcome"`
	Sent                  int64          `json:"sent"`
	Received              int64          `json:"received"`
	HeaderMS              int64          `json:"header_ms"`
	FirstByteMS           int64          `json:"first_byte_ms"`
	TotalMS               int64          `json:"total_ms"`
	UpstreamID            string         `json:"upstream_id"`
	WebSocketConnectionID string         `json:"websocket_connection_id,omitempty"`
}

func (s *store) start(ctx context.Context, r *Record) error {
	r.Provider, r.App = boundedAttribution(r.Provider), boundedAttribution(r.App)
	r.Tag = boundedAttribution(r.Tag)
	billing := "unspecified"
	if r.Money != nil {
		billing = r.Money.Billing
	}
	r.Money = pendingRequestMoney(billing)
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return s.startRequestAndMoney(ctx, r, b)
}

func (s *store) finish(r Record) error {
	r.Provider, r.App = boundedAttribution(r.Provider), boundedAttribution(r.App)
	r.Tag = boundedAttribution(r.Tag)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return s.finishRequestAndMoney(ctx, r, b)
}
func (s *store) records(q url.Values, limit int) ([]Record, int64, error) {
	where, args, err := requestWhere(q)
	if err != nil {
		return nil, 0, err
	}
	args = append(args, limit+1)
	rows, err := s.db.Query("SELECT seq,data FROM requests WHERE "+where+" ORDER BY seq DESC LIMIT ?", args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	return readRequestRecords(rows, limit)
}

func readRequestRecords(rows *sql.Rows, limit int) ([]Record, int64, error) {
	out := []Record{}
	var next int64
	for rows.Next() {
		var seq int64
		var data string
		if err := rows.Scan(&seq, &data); err != nil {
			return nil, 0, err
		}
		if len(out) == limit {
			next = out[len(out)-1].Seq
			break
		}
		var r Record
		if err := json.Unmarshal([]byte(data), &r); err != nil {
			return nil, 0, err
		}
		r.Seq = seq
		if !strings.HasPrefix(r.Path, "/") {
			r.Path = "/" + r.Path
		}
		out = append(out, r)
	}
	return out, next, rows.Err()
}
func (s *store) retain(ctx context.Context, days int) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM requests WHERE seq IN (SELECT seq FROM requests WHERE started<? ORDER BY seq LIMIT 500)", time.Now().Add(-time.Duration(days)*24*time.Hour).UnixMilli())
	if err != nil {
		return err
	}
	return s.retainMoney(ctx)
}

func requestWhere(q url.Values) (string, []any, error) {
	where := []string{"1=1"}
	args := []any{}
	for _, f := range []struct{ key, col string }{{"id", "id"}, {"caller", "caller"}, {"route", "route"}, {"method", "method"}, {"outcome", "outcome"}} {
		if v := q.Get(f.key); v != "" {
			if f.key == "outcome" && v == "active" {
				v = "unknown"
			}
			where = append(where, f.col+"=?")
			args = append(args, v)
		}
	}
	if outcome := q.Get("outcome"); outcome == "active" || outcome == "unknown" {
		ids := q["_active"]
		if len(ids) > 0 {
			placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
			clause := "id IN (" + placeholders + ")"
			if outcome == "unknown" {
				clause = "NOT (" + clause + ")"
			}
			where = append(where, clause)
			for _, id := range ids {
				args = append(args, id)
			}
		} else if outcome == "active" {
			where = append(where, "0=1")
		}
	}
	for _, f := range []struct{ key, clause string }{{"before", "seq < ?"}, {"since", "started >= ?"}, {"until", "started <= ?"}} {
		if v := q.Get(f.key); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return "", nil, fmt.Errorf("invalid filter")
			}
			if n > 0 {
				where = append(where, f.clause)
				args = append(args, n)
			}
		}
	}
	if v := q.Get("path"); v != "" {
		where = append(where, "(instr(CASE WHEN substr(path,1,1)='/' THEN path ELSE '/'||path END,?)>0 OR instr(COALESCE(json_extract(data,'$.local_prefix'),'')||ltrim(path,'/'),?)>0)")
		args = append(args, v, v)
	}
	return strings.Join(where, " AND "), args, nil
}
