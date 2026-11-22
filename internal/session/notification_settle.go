package session

import (
	"context"

	"github.com/term-llm/term-llm/internal/session/notificationdb"
)

func notificationTerminal(t ResponseRunTerminal) notificationdb.ResponseRunTerminal {
	return notificationdb.ResponseRunTerminal{LinkedDeviceID: t.LinkedDeviceID, LinkedToolHandoffMessageID: t.LinkedToolHandoffMessageID, Recoverable: t.Recoverable, PendingClientCallIDs: t.PendingClientCallIDs, ResponseID: t.ResponseID, Outcome: t.Outcome, PendingClientCalls: t.PendingClientCalls, AwaitingAgentJoin: t.AwaitingAgentJoin, AwaitedEvents: notificationAgentFacts(t.AwaitedEvents)}
}
func (s *SQLiteStore) PendingLinkedToolHandoff(ctx context.Context, messageID int64, deviceID string) ([]string, error) {
	return notificationdb.PendingLinkedToolHandoff(ctx, s.queryDB(), messageID, deviceID)
}

func (s *SQLiteStore) ReadLinkedToolReceipt(ctx context.Context, sessionID, deviceID, key string) (notificationdb.LinkedToolReceipt, error) {
	return notificationdb.ReadLinkedToolReceipt(ctx, s.queryDB(), sessionID, deviceID, key)
}
