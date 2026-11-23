package session

import (
	"github.com/term-llm/term-llm/internal/session/searchindex"
	"github.com/term-llm/term-llm/internal/session/searchrank"
)

func migrateSearchIndexV73(db schemaExecutor) error { return searchindex.MigrateV73(db) }

func (s *SQLiteStore) probeSearchMessageIndex() {
	s.hasSearchMessageIndex = searchindex.MatchesV73(s.db)
}

// searchMessageSource names the covering index only when its exact v73 definition exists.
func (s *SQLiteStore) searchMessageSource() searchrank.MessageSource {
	visible, index := searchindex.Source("m", s.hasMessageCompactionTail, s.hasMessageClientID, s.hasSearchMessageIndex)
	return searchrank.MessageSource{Visible: visible, Index: index}
}
