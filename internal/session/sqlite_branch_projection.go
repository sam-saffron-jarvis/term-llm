package session

func (s *SQLiteStore) branchParentProjection(child string) (columns, joins string) {
	if !s.hasSessionBranches {
		return "'', 0", ""
	}
	return "COALESCE(branch_edge.parent_session_id, ''), COALESCE(branch_parent.number, 0)",
		" LEFT JOIN session_branches branch_edge ON branch_edge.child_session_id = " + child +
			" LEFT JOIN sessions branch_parent ON branch_parent.id = branch_edge.parent_session_id"
}

// The existing session SELECT uses unqualified columns; keep those unambiguous.
func (s *SQLiteStore) branchParentSessionColumns() string {
	if !s.hasSessionBranches {
		return "'', 0"
	}
	return `COALESCE((SELECT b.parent_session_id FROM session_branches b WHERE b.child_session_id = sessions.id), ''),
		COALESCE((SELECT p.number FROM session_branches b JOIN sessions p ON p.id = b.parent_session_id WHERE b.child_session_id = sessions.id), 0)`
}

func listSessionSource(opts ListOptions) string {
	if opts.SortByNumberDesc {
		// Keep completed-session walks on the keyset index, avoiding a re-sort.
		return "FROM sessions s INDEXED BY idx_sessions_number"
	}
	return "FROM sessions s"
}
