package session

// The partial covering index saves one lookup into the wide messages table
// per FTS hit. SQLite maintains it for all insert/update/delete paths.
const searchMessageIndexV73 = "idx_messages_search_visible"

func searchVisibleMessageSQL(alias string, hasTail bool) string {
	predicate := "(" + qualifiedMessageColumn(alias, "role") + " = 'assistant' OR " + realUserMessageSQL(alias, false) + ")"
	if hasTail {
		predicate += " AND COALESCE(" + qualifiedMessageColumn(alias, "compaction_tail") + ", FALSE) = FALSE"
	}
	return predicate
}

func migrateSearchIndexV73(db schemaExecutor) error {
	_, err := db.Exec("CREATE INDEX IF NOT EXISTS " + searchMessageIndexV73 + " ON messages(id, session_id, role) WHERE " + searchVisibleMessageSQL("", true))
	return err
}
