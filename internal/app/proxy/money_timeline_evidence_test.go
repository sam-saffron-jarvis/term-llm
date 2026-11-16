package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/app/proxy/usagetap"
)

func freshTimelineAggregate() *timelineAggregate {
	return &timelineAggregate{cost: map[string]int64{}, estimated: map[string]bool{}}
}
func TestTimelineProtocolTokensCountCacheAndReasoningOnce(t *testing.T) {
	for _, tc := range []struct {
		name                                        string
		protocol                                    usagetap.Protocol
		input, read, write, output, reasoning, want int64
	}{
		{"responses", usagetap.Responses, 100, 30, 40, 20, 7, 120},
		{"chat", usagetap.ChatCompletions, 100, 30, 40, 20, 7, 120},
		{"messages", usagetap.Messages, 100, 30, 40, 20, 7, 190},
		{"bedrock", usagetap.Bedrock, 100, 30, 40, 20, 7, 190},
		{"gemini", usagetap.Gemini, 100, 30, 40, 20, 7, 127},
		{"ollama", usagetap.Ollama, 100, -1, -1, 20, -1, 120},
		{"reported zero", usagetap.Responses, 0, 0, 0, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := freshTimelineAggregate()
			rec := MoneyRecord{Usage: &ObservedUsage{Protocol: tc.protocol, Usage: usagetap.Usage{Input: tc.input, CachedInput: tc.read, CacheWrite: tc.write, Output: tc.output, Reasoning: tc.reasoning, Total: 999999, Source: usagetap.Reported}}}
			accumulateTimelineRecord(a, rec, map[string]bool{})
			if !a.tokenKnown || a.tokens != tc.want || a.unknownTokens != 0 {
				t.Fatalf("tokens=%d known=%v missing=%d; want %d", a.tokens, a.tokenKnown, a.unknownTokens, tc.want)
			}
		})
	}
}
func TestTimelineMissingTokenComponentsStayUnknown(t *testing.T) {
	for _, tc := range []struct {
		name     string
		protocol usagetap.Protocol
		usage    usagetap.Usage
	}{
		{"missing cache", usagetap.Responses, usagetap.Usage{Input: 100, CachedInput: -1, Output: 20, Source: usagetap.Reported}},
		{"invalid cache", usagetap.ChatCompletions, usagetap.Usage{Input: 100, CachedInput: 101, Output: 20, Source: usagetap.Reported}},
		{"partial", usagetap.Messages, usagetap.Usage{Input: 100, Output: 20, Source: usagetap.Partial}},
		{"gemini missing thoughts", usagetap.Gemini, usagetap.Usage{Input: 100, Output: 20, Reasoning: -1, Source: usagetap.Reported}},
		{"overflow", usagetap.Ollama, usagetap.Usage{Input: maxMoneyMicros, Output: 1, Source: usagetap.Reported}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := freshTimelineAggregate()
			accumulateTimelineRecord(a, MoneyRecord{Usage: &ObservedUsage{Protocol: tc.protocol, Usage: tc.usage}}, map[string]bool{})
			if a.tokenKnown || a.unknownTokens != 1 {
				t.Fatalf("invented tokens: %+v", a)
			}
		})
	}
}
func TestTimelineCostModesCurrenciesAndFloor(t *testing.T) {
	a := freshTimelineAggregate()
	currencies := map[string]bool{}
	for _, row := range []Money{
		{Billing: "pay_as_you_go", Kind: "estimated", Currency: "USD", Amount: moneyPtr(10)},
		{Billing: "pay_as_you_go", Kind: "charged", Currency: "AUD", Amount: moneyPtr(20)},
		{Billing: "pay_as_you_go", Kind: "estimated_floor", Currency: "USD", Amount: moneyPtr(5)},
		{Billing: "pay_as_you_go", Kind: "no_charge_expected"},
		{Billing: "plan", Kind: "estimated", Currency: "USD", Amount: moneyPtr(900)},
		{Billing: "local", Kind: "charged", Currency: "USD", Amount: moneyPtr(900)},
		{Billing: "unspecified", Kind: "estimated", Currency: "USD", Amount: moneyPtr(900)},
	} {
		row.Source, row.AsOf = "SYNTHETIC test", "2026-11-16"
		accumulateTimelineRecord(a, MoneyRecord{Money: row}, currencies)
	}
	if a.cost["USD"] != 15 || a.cost["AUD"] != 20 || len(currencies) != 2 || a.unknownCost != 2 || a.plan != 1 {
		t.Fatalf("incorrect classification: %+v currencies=%v", a, currencies)
	}
}
func insertTimelineEvidence(t *testing.T, s *Service, id string, at time.Time, row MoneyRecord) {
	t.Helper()
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.db.Exec("INSERT INTO money_history(request_id,started,caller,route,data) VALUES(?,?,?,?,?)", id, at.UnixMilli(), row.DeviceID, row.Provider, string(raw)); err != nil {
		t.Fatal(err)
	}
}
func TestTimelineThirtyDaysCrossMonthClipsBoundsWithoutRowCap(t *testing.T) {
	s := testService(t)
	setTimelineZone(t, s, "Australia/Sydney")
	loc, _ := time.LoadLocation("Australia/Sydney")
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, loc)
	start := time.Date(2026, 10, 4, 0, 0, 0, 0, loc)
	row := MoneyRecord{DeviceID: "fixture", Provider: "openai", Money: Money{Billing: "pay_as_you_go", Kind: "charged", Currency: "USD", Amount: moneyPtr(1), Source: "SYNTHETIC test", AsOf: "2026-11-16"}}
	for i := 0; i < 1100; i++ {
		insertTimelineEvidence(t, s, fmt.Sprintf("row-%04d", i), start.Add(time.Duration(i)*time.Second), row)
	}
	insertTimelineEvidence(t, s, "too-old", start.Add(-time.Millisecond), row)
	insertTimelineEvidence(t, s, "future", now.Add(time.Second), row)
	got, err := s.moneyTimeline(context.Background(), now, "30d", "total")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Buckets) != 30 || !got.Start.Equal(start) || !got.End.Equal(now) {
		t.Fatalf("wrong calendar bounds: %+v", got)
	}
	var total int64
	for _, b := range got.Buckets {
		if b.End.After(now) || b.End.Before(b.Start) {
			t.Fatal("bad bin", b)
		}
		for _, series := range b.Series {
			for _, c := range series.Costs {
				total += c.Micros
			}
		}
	}
	if total != 1100 {
		t.Fatalf("row cap or boundary leak: %d", total)
	}
}
func TestTimelineUsageBackfillPreservesFrozenMoneyAndUnknownFields(t *testing.T) {
	s := testService(t)
	now := time.Now()
	u := &ObservedUsage{Protocol: usagetap.Responses, Usage: usagetap.Usage{Input: 100, CachedInput: 20, Output: 30, Source: usagetap.Reported}}
	rec := Record{ID: "legacy-usage", Started: now.UnixMilli(), CallerID: "device", Route: "route", Usage: u, Money: &Money{Billing: "pay_as_you_go", Kind: "charged", Currency: "USD", Amount: moneyPtr(123)}}
	if err := s.store.start(context.Background(), &rec); err != nil {
		t.Fatal(err)
	}
	rawMoney := `{"billing":"pay_as_you_go","kind":"charged","currency":"USD","amount_micros":123,"future_price_snapshot":{"opaque":"unchanged"}}`
	raw := `{"Started":"` + now.UTC().Format(time.RFC3339Nano) + `","DeviceID":"device","Money":` + rawMoney + `,"future_attribution":{"keep":true}}`
	if _, err := s.store.db.Exec("UPDATE money_history SET data=? WHERE request_id=?", raw, rec.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.store.reconcileMoneyHistory(context.Background(), now.Add(-time.Hour).UnixMilli(), now.Add(time.Hour).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	var after string
	if err := s.store.db.QueryRow("SELECT data FROM money_history WHERE request_id=?", rec.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(after), &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["Money"]) != rawMoney || !strings.Contains(string(fields["future_attribution"]), "keep") || len(fields["usage"]) == 0 {
		t.Fatalf("backfill changed frozen evidence: %s", after)
	}
	var mr MoneyRecord
	if err := json.Unmarshal([]byte(after), &mr); err != nil || mr.Usage == nil || mr.Usage.Usage.Input != 100 {
		t.Fatal("usage not backfilled", mr, err)
	}
}
func TestTimelineDurableUsageSurvivesTechnicalRetention(t *testing.T) {
	s := testService(t)
	now := time.Now()
	row := Record{ID: "durable-tokens", Started: now.UnixMilli(), CallerID: "device", Route: "route", Outcome: "complete", Usage: &ObservedUsage{Protocol: usagetap.Messages, Usage: usagetap.Usage{Input: 10, CachedInput: 3, CacheWrite: 2, Output: 5, Source: usagetap.Reported}}, Money: pendingRequestMoney("plan")}
	if err := s.store.start(context.Background(), &row); err != nil {
		t.Fatal(err)
	}
	if err := s.store.finish(row); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec("DELETE FROM requests WHERE id=?", row.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.moneyTimeline(context.Background(), now.Add(time.Second), "today", "total")
	if err != nil {
		t.Fatal(err)
	}
	var tokens int64
	for _, b := range got.Buckets {
		for _, x := range b.Series {
			if x.Tokens != nil {
				tokens += *x.Tokens
			}
		}
	}
	if tokens != 20 {
		t.Fatalf("usage disappeared with technical logs: %d", tokens)
	}
}
func TestTimelineAPIValidatesQueryAndMethod(t *testing.T) {
	s := testService(t)
	for _, tc := range []struct {
		method, path string
		want         int
	}{{"GET", "/_proxy/api/timeline?range=year", 400}, {"GET", "/_proxy/api/timeline?group=app", 400}, {"POST", "/_proxy/api/timeline", 405}, {"GET", "/_proxy/api/timeline", 200}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if !s.moneyAPI(w, r, "timeline") || w.Code != tc.want {
			t.Fatalf("%s %s status=%d want=%d", tc.method, tc.path, w.Code, tc.want)
		}
		if w.Code == http.StatusOK && strings.Contains(w.Body.String(), "requests") {
			t.Fatal("technical request body leaked", w.Body.String())
		}
	}
}

func TestTimelineInvalidCostProvenanceStaysUnknown(t *testing.T) {
	for _, m := range []Money{
		{Billing: "pay_as_you_go", Kind: "estimated", Currency: "USD", Amount: moneyPtr(10)},
		{Billing: "pay_as_you_go", Kind: "charged", Currency: "USD", Amount: moneyPtr(10), Source: "fixture", AsOf: "not-a-date"},
	} {
		a := freshTimelineAggregate()
		accumulateTimelineRecord(a, MoneyRecord{Money: m}, map[string]bool{})
		if len(a.cost) != 0 || a.unknownCost != 1 {
			t.Fatalf("invalid price evidence counted: %+v", a)
		}
	}
}

func TestTimelineSparseBinsStillUseGlobalTopFivePlusOther(t *testing.T) {
	aggs := make([]map[string]*timelineAggregate, 8)
	result := moneyTimeline{Buckets: make([]timelineBucket, 8), Currencies: []string{"USD"}}
	for i := range aggs {
		a := freshTimelineAggregate()
		a.requests = 1
		a.label = fmt.Sprintf("Client %d", i)
		a.cost["USD"] = 1
		aggs[i] = map[string]*timelineAggregate{fmt.Sprintf("client-%d", i): a}
	}
	finalizeTimelineBuckets(&result, aggs)
	keys := map[string]bool{}
	var amount int64
	for _, bucket := range result.Buckets {
		for _, s := range bucket.Series {
			keys[s.Key] = true
			for _, c := range s.Costs {
				amount += c.Micros
			}
		}
	}
	if len(keys) != 6 || !keys[timelineOtherKey] || amount != 8 {
		t.Fatalf("sparse bins bypassed global grouping: keys=%v amount=%d", keys, amount)
	}
}
