package proxy

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
)

type store struct {
	db            *sql.DB
	authorizeStmt *sql.Stmt
	startStmt     *sql.Stmt
	finishStmt    *sql.Stmt
	moneyStmt     *sql.Stmt
	closeOnce     sync.Once
	closeErr      error
}

// prepareStatements runs once after schema initialization, before the store is
// published. Reuse SQL plans, not authorization results: every admission still
// reads the current caller and exact route authority from the database.
// On failure the store, including any statements already prepared, is closed.
func (s *store) prepareStatements() error {
	for _, item := range []struct {
		name  string
		query string
		dest  **sql.Stmt
	}{
		{"authorization", "SELECT c.id,c.name FROM callers c JOIN grants g ON c.id=g.caller WHERE c.hash=? AND c.revoked=0 AND g.route=? AND g.authority=?", &s.authorizeStmt},
		{"request start", "INSERT INTO requests(id,started,caller,route,method,path,outcome,data) VALUES(?,?,?,?,?,?,?,?)", &s.startStmt},
		{"request finish", "UPDATE requests SET outcome=?,data=? WHERE id=?", &s.finishStmt},
		{"money history", "INSERT INTO money_history(request_id,started,caller,route,data) VALUES(?,?,?,?,?) ON CONFLICT(request_id) DO UPDATE SET data=excluded.data", &s.moneyStmt},
	} {
		stmt, err := s.db.Prepare(item.query)
		if err != nil {
			return errors.Join(fmt.Errorf("prepare proxy %s statement: %w", item.name, err), s.close())
		}
		*item.dest = stmt
	}
	return nil
}

// close releases partially or fully initialized statements before the database.
// Fields remain immutable after startup so calls concurrent with shutdown fail
// through database/sql rather than racing on statement pointers.
func (s *store) close() error {
	s.closeOnce.Do(func() {
		for _, item := range []struct {
			name string
			stmt *sql.Stmt
		}{
			{"authorization", s.authorizeStmt},
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
		if s.db != nil {
			if err := s.db.Close(); err != nil {
				s.closeErr = errors.Join(s.closeErr, fmt.Errorf("close proxy store: %w", err))
			}
		}
	})
	return s.closeErr
}
