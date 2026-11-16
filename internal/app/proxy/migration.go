package proxy

import (
	"context"
	"fmt"
	"log"

	"github.com/term-llm/term-llm/internal/sqliteutil"
)

const schemaVersion = 4

// Version 1 was the initial acceptance build, not a released schema. Its grants
// knew only an origin, so their original delegated base/key scope cannot be
// reconstructed safely. Keep callers/tokens/history, but require explicit regrant.
var migrations = []sqliteutil.Migration{{
	Version: 2, Description: "bind caller grants to exact route authority",
	Up: func(tx sqliteutil.Executor) error {
		if _, err := tx.Exec("ALTER TABLE grants RENAME COLUMN origin TO authority"); err != nil {
			return err
		}
		_, err := tx.Exec("DELETE FROM grants")
		return err
	},
}, {Version: 3, Description: "durable renewable account custody", Up: func(tx sqliteutil.Executor) error {
	if _, err := tx.Exec(accountSchema); err != nil {
		return err
	}
	return initializeMoneySchema(tx)
}}, {Version: 4, Description: "bounded fresh-approved Claude connect receipts", Up: func(tx sqliteutil.Executor) error { _, err := tx.Exec(connectSchema); return err }},
}

func (s *store) initialize() error {
	var invalidated int
	err := sqliteutil.WithImmediateMigrationTx(context.Background(), s.db, func(tx sqliteutil.Executor) error {
		var v int
		if err := tx.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
			return err
		}
		if v == schemaVersion {
			return nil
		}
		if v < 0 || v > schemaVersion {
			return fmt.Errorf("proxy schema version %d is not supported (maximum %d)", v, schemaVersion)
		}
		if v == 0 {
			return createSchema(tx)
		}
		if v < 2 {
			if err := tx.QueryRow("SELECT count(*) FROM grants").Scan(&invalidated); err != nil {
				return err
			}
		}
		for _, migration := range migrations {
			if migration.Version <= v {
				continue
			}
			if err := migration.Up(tx); err != nil {
				return fmt.Errorf("proxy migration %d (%s): %w", migration.Version, migration.Description, err)
			}
			if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version=%d", migration.Version)); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil && invalidated > 0 {
		log.Printf("proxy: schema upgrade removed %d origin-only grants; client tokens remain valid after explicit regrant in Clients", invalidated)
	}
	return err
}
func createSchema(tx sqliteutil.Executor) error {
	var n int
	if err := tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return fmt.Errorf("unknown unversioned proxy schema")
	}
	if _, err := tx.Exec(schema); err != nil {
		return err
	}
	return nil
}
