package proxy

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/term-llm/term-llm/internal/app/proxy/ledger"
)

type store struct {
	db                            *sql.DB
	readDB                        *sql.DB
	settingsStmt, streamGrantStmt *sql.Stmt
	authorizeStmt                 *sql.Stmt
	startStmt                     *sql.Stmt
	finishStmt                    *sql.Stmt
	moneyStmt                     *sql.Stmt
	ledger                        *ledger.Writer
	closeOnce                     sync.Once
	closeErr                      error
}

// prepareStatements runs once after schema initialization, before the store is
// published. Reuse SQL plans, not authorization results: every admission still
// reads the current caller and exact route authority from the database.
// On failure the store, including any statements already prepared, is closed.
func (s *store) prepareStatements() error {
	read := s.readDB
	if read == nil { // Same connection for an isolated in-memory store.
		read = s.db
	}
	for _, item := range []struct {
		pool  *sql.DB
		name  string
		query string
		dest  **sql.Stmt
	}{
		{read, "authorization", "SELECT c.id,c.name FROM callers c JOIN grants g ON c.id=g.caller WHERE c.hash=? AND c.revoked=0 AND g.route=? AND g.authority=?", &s.authorizeStmt},
		{read, "price settings", "SELECT data FROM money_settings WHERE id=1", &s.settingsStmt},
		{read, "stream authorization", "SELECT c.id FROM callers c JOIN grants g ON c.id=g.caller WHERE c.id=? AND c.revoked=0 AND g.route=? AND g.authority=?", &s.streamGrantStmt},
		{s.db, "request start", "INSERT INTO requests(id,started,caller,route,method,path,outcome,data) VALUES(?,?,?,?,?,?,?,?)", &s.startStmt},
		{s.db, "request finish", "UPDATE requests SET outcome=?,data=? WHERE id=?", &s.finishStmt},
		{s.db, "money history", "INSERT INTO money_history(request_id,started,caller,route,data) VALUES(?,?,?,?,?) ON CONFLICT(request_id) DO UPDATE SET data=excluded.data", &s.moneyStmt},
	} {
		stmt, err := item.pool.Prepare(item.query)
		if err != nil {
			return errors.Join(fmt.Errorf("prepare proxy %s statement: %w", item.name, err), s.close())
		}
		*item.dest = stmt
	}
	s.ledger = ledger.New(s.db, s.startStmt, s.finishStmt, s.moneyStmt)
	return nil
}

// close releases partially or fully initialized statements before the database.
// Fields remain immutable after startup so calls concurrent with shutdown fail
// through database/sql rather than racing on statement pointers.
func (s *store) close() error {
	s.closeOnce.Do(func() {
		if s.ledger != nil {
			s.ledger.Stop()
		}
		for _, item := range []struct {
			name string
			stmt *sql.Stmt
		}{
			{"authorization", s.authorizeStmt},
			{"price settings", s.settingsStmt},
			{"stream authorization", s.streamGrantStmt},
			{"request start", s.startStmt},
			{"request finish", s.finishStmt},
			{"money history", s.moneyStmt},
		} {
			if item.stmt != nil {
				if err := item.stmt.Close(); err != nil {
					s.closeErr = errors.Join(s.closeErr, fmt.Errorf("close proxy %s statement: %w", item.name, err))
				}
			}
		}
		if s.readDB != nil {
			s.closeErr = errors.Join(s.closeErr, s.readDB.Close())
		}
		if s.db != nil {
			if err := s.db.Close(); err != nil {
				s.closeErr = errors.Join(s.closeErr, fmt.Errorf("close proxy store: %w", err))
			}
		}
		if s.ledger != nil {
			s.ledger.Wait()
		}
	})
	return s.closeErr
}
