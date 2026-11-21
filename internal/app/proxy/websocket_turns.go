package proxy

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/term-llm/term-llm/internal/app/proxy/usagetap"
)

// A connection keeps its admission row until its first observed turn commits.
// Frame observation only enqueues small metadata; it never waits on SQLite.
// The channel fits the entire bounded set of turns observed on one socket.
func (s *Service) logWebSocketTurns(resp *http.Response, connection Record, route Route) func() bool {
	duplex, ok := resp.Body.(*usageObserverDuplex)
	if !ok || duplex.disabled || duplex.protocol != usagetap.Responses {
		return nil
	}
	tap, ok := duplex.tap.(*usagetap.WebSocket)
	if !ok {
		return nil
	}
	turns := make(chan usagetap.WebSocketTurn, usagetap.MaxWebSocketTurns)
	tap.SetTurnObserver(func(turn usagetap.WebSocketTurn) { turns <- turn })
	done := make(chan bool, 1)
	go func() {
		persisted := false
		for turn := range turns {
			record := webSocketTurnRecord(connection, turn)
			s.finalizeMoney(&record, route)
			if err := s.store.insertWebSocketTurn(record, !persisted); err != nil {
				log.Print("proxy: WebSocket turn ledger write failed")
				continue
			}
			persisted = true
		}
		done <- persisted
	}()
	return func() bool {
		close(turns)
		return <-done
	}
}

func webSocketTurnRecord(connection Record, turn usagetap.WebSocketTurn) Record {
	rec := connection
	rec.ID = randomToken("px_")[:35]
	rec.Seq = 0
	rec.WebSocketConnectionID = connection.ID
	rec.Status = http.StatusSwitchingProtocols
	rec.Started, rec.Ended = turn.Started.UnixMilli(), turn.Ended.UnixMilli()
	rec.Outcome = turn.Outcome
	rec.Usage = &ObservedUsage{Usage: turn.Usage, Protocol: usagetap.Responses}
	// The handshake and wire counters describe the connection, not this turn.
	rec.UpstreamID, rec.HeaderMS, rec.FirstByteMS, rec.Sent, rec.Received = "", -1, -1, -1, -1
	rec.TotalMS = -1
	if turn.Started.IsZero() {
		rec.Started = rec.Ended
	} else {
		rec.TotalMS = turn.Ended.Sub(turn.Started).Milliseconds()
	}
	return rec
}

func (s *Service) finishDispatchRecord(rec *Record, route Route) {
	s.finalizeMoney(rec, route)
	if rec.Outcome == "not_dispatched" {
		// Never use this evidence for failures after inference was dispatched.
		money := CalculateMoney(MoneyInput{Billing: route.Billing, RefusedBeforeDispatch: true}, nil)
		rec.Money = &money
	}
	if err := s.store.finish(*rec); err != nil {
		log.Print("proxy: terminal ledger write failed; outcome remains unknown")
	}
}

func (s *store) insertWebSocketTurn(rec Record, replaceConnection bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	moneyData, err := marshalMoneyRecord(rec)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.StmtContext(ctx, s.startStmt).ExecContext(ctx, rec.ID, rec.Started, rec.CallerID, rec.Route, rec.Method, rec.Path, rec.Outcome, string(data)); err != nil {
		return err
	}
	if err := writeMoneyRecord(ctx, tx.StmtContext(ctx, s.moneyStmt), rec, moneyData); err != nil {
		return err
	}
	if replaceConnection {
		if _, err := tx.ExecContext(ctx, "DELETE FROM requests WHERE id=?", rec.WebSocketConnectionID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM money_history WHERE request_id=?", rec.WebSocketConnectionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
