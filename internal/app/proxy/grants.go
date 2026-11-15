package proxy

import (
	"database/sql"
)

type inputError string

func (e inputError) Error() string { return string(e) }

// writeGrants is shared by create and edit; it never reissues a caller token.
func writeGrants(tx *sql.Tx, id string, grants []string, snap *snapshot) error {
	if len(grants) > 128 {
		return inputError("at most 128 route grants per caller")
	}
	seen := map[string]bool{}
	for _, name := range grants {
		if !slug.MatchString(name) || seen[name] {
			return inputError("route grants must contain unique valid route names")
		}
		seen[name] = true
		var authority string
		for _, r := range snap.routes {
			if r.Name == name {
				authority = r.authority
				break
			}
		}
		if authority == "" {
			return inputError("route grant is not in the active configuration; refresh before saving")
		}
		if _, err := tx.Exec("INSERT INTO grants(caller,route,authority) VALUES(?,?,?)", id, name, authority); err != nil {
			return err
		}
	}
	return nil
}
func (s *store) updateGrants(id string, grants []string, snap *snapshot) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var revoked bool
	if err := tx.QueryRow("SELECT revoked FROM callers WHERE id=?", id).Scan(&revoked); err == sql.ErrNoRows {
		return inputError("caller no longer exists; refresh before editing grants")
	} else if err != nil {
		return err
	}
	if revoked {
		return inputError("revoked callers cannot receive new grants; create a new caller")
	}
	if _, err := tx.Exec("DELETE FROM grants WHERE caller=?", id); err != nil {
		return err
	}
	if err := writeGrants(tx, id, grants, snap); err != nil {
		return err
	}
	return tx.Commit()
}
