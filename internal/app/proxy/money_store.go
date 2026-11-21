package proxy

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/term-llm/term-llm/internal/app/proxy/ledger"
	"github.com/term-llm/term-llm/internal/app/proxy/usagetap"
	"github.com/term-llm/term-llm/internal/sqliteutil"
)

const moneySchema = `
CREATE TABLE money_settings(id INTEGER PRIMARY KEY CHECK(id=1),data TEXT NOT NULL);
INSERT INTO money_settings VALUES(1,'{}');
CREATE TABLE money_history(request_id TEXT PRIMARY KEY,started INTEGER NOT NULL,caller TEXT NOT NULL,route TEXT NOT NULL,data TEXT NOT NULL);
CREATE INDEX money_history_started ON money_history(started,request_id);
`

// initializeMoneySchema runs only inside the versioned schema transaction. Old
// request records are preserved as explicitly unspecified billing, never $0.
func initializeMoneySchema(tx sqliteutil.Executor) error {
	if _, err := tx.Exec(moneySchema); err != nil {
		return err
	}
	var after int64
	for {
		rows, err := tx.Query("SELECT seq,id,started,caller,route,data FROM requests WHERE seq>? ORDER BY seq LIMIT 128", after)
		if err != nil {
			return err
		}
		type oldRecord struct {
			id     string
			route  string
			record MoneyRecord
		}
		records := []oldRecord{}
		for rows.Next() {
			var id, data string
			var fallback Record
			if err := rows.Scan(&after, &id, &fallback.Started, &fallback.CallerID, &fallback.Route, &data); err != nil {
				rows.Close()
				return err
			}
			var record Record
			if json.Unmarshal([]byte(data), &record) != nil {
				record = fallback
				record.Money = pendingRequestMoney("unspecified")
			}
			record.ID, record.Started, record.CallerID, record.Route = id, fallback.Started, fallback.CallerID, fallback.Route
			records = append(records, oldRecord{id: id, route: record.Route, record: requestMoneyRecord(record)})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, item := range records {
			data, err := json.Marshal(item.record)
			if err != nil {
				return err
			}
			if _, err = tx.Exec("INSERT INTO money_history(request_id,started,caller,route,data) VALUES(?,?,?,?,?)", item.id, item.record.Started.UnixMilli(), item.record.DeviceID, item.route, string(data)); err != nil {
				return err
			}
		}
		if len(records) < 128 {
			break
		}
	}
	return nil
}

func requestMoneyRecord(rec Record) MoneyRecord {
	money := Money{Billing: "unspecified", Kind: "unspecified", Reason: "Recorded before billing was configured."}
	if rec.Money != nil {
		money = *rec.Money
	}
	provider := rec.Provider
	if provider == "" {
		provider = rec.Route
	}
	return MoneyRecord{Started: time.UnixMilli(rec.Started), DeviceID: rec.CallerID, Device: rec.CallerName, Tag: rec.Tag, App: rec.App, Provider: provider, Money: money, Usage: rec.Usage}
}

func marshalMoneyRecord(rec Record) (string, error) {
	data, err := json.Marshal(requestMoneyRecord(rec))
	return string(data), err
}

// The transaction-bound upsert replaces only the evidence, never its index keys.
func writeMoneyRecord(ctx context.Context, stmt *sql.Stmt, rec Record, data string) error {
	_, err := stmt.ExecContext(ctx, rec.ID, rec.Started, rec.CallerID, rec.Route, data)
	return err
}

func (s *store) finishRequestAndMoney(ctx context.Context, rec Record, data []byte) error {
	moneyData, err := marshalMoneyRecord(rec)
	if err != nil {
		return err
	}
	return s.ledger.Finish(ctx, ledgerPair(rec, data, moneyData))
}

func ledgerPair(rec Record, data []byte, money string) ledger.Pair {
	return ledger.Pair{ID: rec.ID, Started: rec.Started, CallerID: rec.CallerID, Route: rec.Route, Method: rec.Method, Path: rec.Path, Outcome: rec.Outcome, Data: string(data), Money: money}
}

// pendingRequestMoney is also the admission hook: the server should attach this
// using its active Route.Billing before store.start. Never infer billing from a
// stored config in file mode, where that config may not be the active authority.
func pendingRequestMoney(billing string) *Money {
	if billing != "plan" && billing != "local" && billing != "pay_as_you_go" {
		billing = "unspecified"
	}
	return &Money{Billing: billing, Kind: "unmeasured", Reason: "In flight or outcome unknown; usage and any provider charge are not yet known."}
}
func (s *store) startRequestAndMoney(ctx context.Context, rec *Record, data []byte) error {
	moneyData, err := marshalMoneyRecord(*rec)
	if err != nil {
		return err
	}
	seq, err := s.ledger.Start(ctx, ledgerPair(*rec, data, moneyData))
	if err == nil {
		rec.Seq = seq
	}
	return err
}

// Reconcile missing pre-fix money rows without repricing terminal evidence.
// Called before monthly reads; the host may additionally call it at open with
// the full retained interval. Batches close SQL rows before writes on one conn.
func (s *store) reconcileMoneyHistory(ctx context.Context, since, until int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	moneyStmt := tx.StmtContext(ctx, s.moneyStmt)
	var after int64
	for {
		rows, err := tx.QueryContext(ctx, `SELECT r.seq,r.id,r.started,r.caller,r.route,r.data FROM requests r LEFT JOIN money_history m ON m.request_id=r.id WHERE r.seq>? AND r.started>=? AND r.started<=? AND m.request_id IS NULL ORDER BY r.seq LIMIT 128`, after, since, until)
		if err != nil {
			return err
		}
		records := []Record{}
		for rows.Next() {
			var rec Record
			var raw string
			if err = rows.Scan(&after, &rec.ID, &rec.Started, &rec.CallerID, &rec.Route, &raw); err != nil {
				rows.Close()
				return err
			}
			fallback := rec
			if json.Unmarshal([]byte(raw), &rec) != nil {
				rec = fallback
			}
			rec.ID, rec.Started, rec.CallerID, rec.Route = fallback.ID, fallback.Started, fallback.CallerID, fallback.Route
			if rec.Money == nil || rec.Outcome == "unknown" || rec.Outcome == "active" || rec.Outcome == "" {
				billing := "unspecified"
				if rec.Money != nil {
					billing = rec.Money.Billing
				}
				rec.Money = pendingRequestMoney(billing)
			}
			records = append(records, rec)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, rec := range records {
			moneyData, err := marshalMoneyRecord(rec)
			if err != nil {
				return err
			}
			if err = writeMoneyRecord(ctx, moneyStmt, rec, moneyData); err != nil {
				return err
			}
		}
		if len(records) < 128 {
			break
		}
	}
	if err = backfillMoneyUsage(tx, since, until); err != nil {
		return err
	}
	return tx.Commit()
}

type usageBackfill struct{ id, data string }

func backfillMoneyUsage(tx sqliteutil.Executor, since, until int64) error {
	// Preserve unknown/future fields and the exact frozen Money JSON fragment.
	backfillRows, err := tx.Query(`SELECT m.request_id,m.data,r.data FROM money_history m JOIN requests r ON r.id=m.request_id WHERE m.started>=? AND m.started<=?`, since, until)
	if err != nil {
		return err
	}
	updates := []usageBackfill{}
	for backfillRows.Next() {
		update, err := prepareUsageBackfill(backfillRows)
		if err != nil {
			backfillRows.Close()
			return err
		}
		if update.id != "" {
			updates = append(updates, update)
		}
	}
	if err = backfillRows.Err(); err != nil {
		backfillRows.Close()
		return err
	}
	backfillRows.Close()
	for _, update := range updates {
		if _, err = tx.Exec("UPDATE money_history SET data=? WHERE request_id=?", update.data, update.id); err != nil {
			return err
		}
	}
	return nil
}

func prepareUsageBackfill(rows interface{ Scan(...any) error }) (usageBackfill, error) {
	var id, moneyData, requestData string
	if err := rows.Scan(&id, &moneyData, &requestData); err != nil {
		return usageBackfill{}, err
	}
	var fields map[string]json.RawMessage
	var requestRecord Record
	if json.Unmarshal([]byte(moneyData), &fields) != nil || fields == nil || fields["usage"] != nil || json.Unmarshal([]byte(requestData), &requestRecord) != nil || requestRecord.Usage == nil {
		return usageBackfill{}, nil
	}
	usageJSON, err := json.Marshal(requestRecord.Usage)
	if err != nil {
		return usageBackfill{}, err
	}
	original := bytes.TrimSpace([]byte(moneyData))
	if len(original) < 2 || original[len(original)-1] != '}' {
		return usageBackfill{}, fmt.Errorf("invalid money history object")
	}
	insert := []byte(`,"usage":`)
	if len(fields) == 0 {
		insert = []byte(`"usage":`)
	}
	updated := make([]byte, 0, len(original)+len(insert)+len(usageJSON))
	updated = append(updated, original[:len(original)-1]...)
	updated = append(updated, insert...)
	updated = append(updated, usageJSON...)
	updated = append(updated, '}')
	return usageBackfill{id, string(updated)}, nil
}
func (s *store) loadMoneySettings(ctx context.Context) (MoneySettings, error) {
	var data string
	if err := s.settingsStmt.QueryRowContext(ctx).Scan(&data); err != nil {
		return MoneySettings{}, err
	}
	var settings MoneySettings
	if err := json.Unmarshal([]byte(data), &settings); err != nil {
		return settings, err
	}
	return settings, ValidateMoneySettings(settings)
}
func (s *Service) currentMoneySettings(ctx context.Context) (MoneySettings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentMoneySettingsLocked(ctx)
}
func (s *Service) currentMoneySettingsLocked(ctx context.Context) (MoneySettings, error) {
	if s.opts.ConfigFile != "" {
		return s.snap.config.Money, nil
	}
	return s.store.loadMoneySettings(ctx)
}

// The table is canonical in DB mode. Config mirrors it in the same transaction
// so imports, revisions, grant reconciliation and money changes commit together.
func (s *store) saveConfigAndMoney(snap *snapshot) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Browser and API edits are the administrator's decision; keep approvals.
	if err := rebindGrants(tx, snap); err != nil {
		return err
	}
	dropped, err := reconcileGrants(tx, snap)
	if err != nil {
		return err
	}
	config, err := json.Marshal(snap.config)
	if err != nil {
		return err
	}
	settings, err := json.Marshal(snap.config.Money)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE configuration SET revision=?,data=? WHERE id=1", snap.revision, string(config)); err != nil {
		return err
	}
	result, err := tx.Exec("UPDATE money_settings SET data=? WHERE id=1", string(settings))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("money settings singleton missing")
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if dropped > 0 {
		log.Printf("proxy: activation removed %d incompatible route grants; regrant explicitly in Clients", dropped)
	}
	return nil
}
func (s *Service) moneySummary(ctx context.Context, now time.Time) (MoneySummary, error) {
	settings, err := s.currentMoneySettings(ctx)
	if err != nil {
		return MoneySummary{}, err
	}
	builder, err := newMoneySummaryBuilder(now, settings)
	if err != nil {
		return MoneySummary{}, err
	}
	if err = s.store.reconcileMoneyHistory(ctx, builder.month.UnixMilli(), now.UnixMilli()); err != nil {
		return MoneySummary{}, err
	}
	rows, err := s.store.db.QueryContext(ctx, "SELECT started,caller,route,data FROM money_history WHERE started>=? AND started<=? ORDER BY started,request_id", builder.month.UnixMilli(), now.UnixMilli())
	if err != nil {
		return MoneySummary{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var started int64
		var caller, route, data string
		if err = rows.Scan(&started, &caller, &route, &data); err != nil {
			return MoneySummary{}, err
		}
		var row MoneyRecord
		if json.Unmarshal([]byte(data), &row) != nil || row.Money.Billing == "" || row.Money.Kind == "" {
			row = MoneyRecord{Money: *pendingRequestMoney("unspecified")}
		}
		row.Started = time.UnixMilli(started)
		if row.DeviceID == "" {
			row.DeviceID = caller
		}
		if row.Provider == "" {
			row.Provider = route
		}
		if err = builder.add(row); err != nil {
			return MoneySummary{}, err
		}
	}
	if err = rows.Err(); err != nil {
		return MoneySummary{}, err
	}
	return builder.finish(), nil
}

// moneyAPI is called only after existing administrator session/Origin/CSRF checks.
func (s *Service) moneyAPI(w http.ResponseWriter, r *http.Request, path string) bool {
	if path == "timeline" {
		if r.Method != http.MethodGet {
			fail(w, 405, "Method not allowed")
			return true
		}
		return s.timelineAPI(w, r)
	}
	if path != "summary" && path != "money" {
		return false
	}
	timeout := 3 * time.Second
	if path == "summary" {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	if path == "summary" && r.Method == http.MethodGet {
		summary, err := s.moneySummary(ctx, time.Now())
		if err != nil {
			fail(w, 503, "Spending summary is unavailable; nothing has been assumed to cost zero")
			return true
		}
		jsonReply(w, 200, summary)
		return true
	}
	if path == "money" && r.Method == http.MethodGet {
		settings, err := s.currentMoneySettings(ctx)
		if err != nil {
			fail(w, 503, "Money settings unavailable")
			return true
		}
		jsonReply(w, 200, settings)
		return true
	}
	if path == "money" && r.Method == http.MethodPut {
		if s.opts.ConfigFile != "" {
			fail(w, 409, "Money settings are managed in the operator configuration file")
			return true
		}
		var settings MoneySettings
		if !readJSON(w, r, &settings) {
			return true
		}
		if err := ValidateMoneySettings(settings); err != nil {
			fail(w, 400, "Check the currency, warning budget, timezone and exact dated price entries")
			return true
		}
		s.mu.Lock()
		next := *s.snap
		next.config.Money = settings
		next.revision++
		err := s.store.saveConfigAndMoney(&next)
		if err == nil {
			s.snap = &next
		}
		s.mu.Unlock()
		if err != nil {
			fail(w, 503, "Could not save money settings; active settings unchanged")
			return true
		}
		jsonReply(w, 200, map[string]int64{"revision": next.revision})
		return true
	}
	fail(w, 405, "Method not allowed")
	return true
}

// finalizeMoney freezes the current price evidence at request finish. Tokens are
// provider assertions, not bills. A missing or malformed component never becomes
// an invented zero, and configured plan traffic is not proof of zero overage.
func (s *Service) finalizeMoney(rec *Record, route Route) {
	rec.Provider = route.Name
	input := MoneyInput{Billing: route.Billing, Provider: route.Name, UsageState: "unknown", RefusedBeforeDispatch: rec.Outcome == "denied"}
	if money := reportedVeniceMoney(rec, input); money != nil {
		rec.Money = money
		return
	}
	if rec.Usage != nil {
		input = moneyInputFromUsage(input, *rec.Usage)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	settings, err := s.currentMoneySettings(ctx)
	if err != nil {
		rec.Money = &Money{Billing: route.Billing, Kind: "unmeasured", Reason: "Price settings unavailable; no zero assumed."}
		return
	}
	money := finishRequestMoneyUsingCatalog(input, route, rec.Usage, settings.Rates, s.catalogMoney(route))
	rec.Money = &money
}

// reportedVeniceMoney only consumes the usage observer's trusted Venice field.
// It does not infer billing from the route's name or change its configuration.
// A reported amount (including zero) does not depend on any token rate card.
func reportedVeniceMoney(rec *Record, in MoneyInput) *Money {
	if rec.Outcome != "complete" || rec.Status < 200 || rec.Status >= 300 || rec.Ended <= 0 || rec.Usage == nil {
		return nil
	}
	amount := rec.Usage.Usage.VeniceCostMicros
	if amount == nil {
		return nil
	}
	switch in.Billing {
	case "", "unspecified":
		in.Billing = "pay_as_you_go"
	case "pay_as_you_go":
	default:
		return nil
	}
	in.Charge = &MoneyCharge{
		AmountMicros: *amount,
		Currency:     "USD",
		Source:       "Venice reported usage.cost (1 DIEM = 1 USD)",
		AsOf:         time.UnixMilli(rec.Ended).UTC().Format(time.DateOnly),
	}
	money := CalculateMoney(in, nil)
	if money.Kind != "charged" {
		return nil
	}
	return &money
}

func moneyInputFromUsage(in MoneyInput, observed ObservedUsage) MoneyInput {
	u := observed.Usage
	in.Model = u.Model
	in.Tier = u.Tier
	if u.Ambiguous {
		in.Model = ""
	}
	in.UsageState = string(u.Source)
	in.InputTokens = u.Input
	in.OutputTokens = u.Output
	in.CacheReadTokens = u.CachedInput
	in.CacheWriteTokens = u.CacheWrite
	// Missing cache/output counts can be significant. Only a known zero may be
	// used as zero; partially observed fresh input is a Messages lower bound.
	switch observed.Protocol {
	case usagetap.Messages, usagetap.Bedrock:
		if u.Source == usagetap.Partial {
			if in.InputTokens < 0 {
				in.InputTokens = 0
			}
			if in.OutputTokens < 0 {
				in.OutputTokens = 0
			}
			if in.CacheReadTokens < 0 {
				in.CacheReadTokens = 0
			}
			if in.CacheWriteTokens < 0 {
				in.CacheWriteTokens = 0
			}
		}
	case usagetap.Responses, usagetap.ChatCompletions, usagetap.Gemini:
		if u.CachedInput >= 0 && u.Input >= u.CachedInput {
			in.InputTokens = u.Input - u.CachedInput
		} else {
			in.InputTokens = -1
		}
		if u.Input >= 0 && u.CachedInput > u.Input {
			in.UsageState = "unknown"
		}
		if u.Source == usagetap.Partial {
			if in.InputTokens < 0 {
				in.InputTokens = 0
			}
			if in.OutputTokens < 0 {
				in.OutputTokens = 0
			}
			if in.CacheReadTokens < 0 {
				in.CacheReadTokens = 0
			}
		}
		// These protocols report cache-write accounting inside input, not a separate
		// Anthropic-style billable component. No extra cache-write charge is added.
		in.CacheWriteTokens = 0
		if observed.Protocol == usagetap.Gemini {
			if u.Reasoning >= 0 && in.OutputTokens >= 0 && u.Reasoning <= 9223372036854775807-in.OutputTokens {
				in.OutputTokens += u.Reasoning
			} else if u.Reasoning < 0 {
				in.OutputTokens = -1
			}
		}
	case usagetap.Ollama:
		in.CacheReadTokens = 0
		in.CacheWriteTokens = 0
	}
	if in.InputTokens < 0 || in.OutputTokens < 0 || in.CacheReadTokens < 0 || in.CacheWriteTokens < 0 {
		in.UsageState = "unknown"
	}
	return in
}

// Retain financial evidence separately from short-lived technical request logs.
func (s *store) retainMoney(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM money_history WHERE request_id IN (SELECT request_id FROM money_history WHERE started<? ORDER BY started LIMIT 500)", time.Now().AddDate(0, 0, -400).UnixMilli())
	return err
}
