package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMoneySummaryComesFromForwardedTrafficAndSurvivesTechnicalRetention(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("X-Upstream-Key") != "Real vendor-canary" {
			t.Error("wrong upstream authentication")
		}
		if r.Header.Get("X-Term-LLM-Proxy-App") != "" {
			t.Error("app attribution leaked upstream")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"synthetic-id\",\"model\":\"fixture-large\",\"usage\":{\"input_tokens\":12400,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":850,\"total_tokens\":13250}}}\n\n")
	}))
	defer upstream.Close()
	s := testService(t)
	api := route("api", "/api/", upstream.URL, "X-Local", "vendor")
	api.Billing = "pay_as_you_go"
	api.Usage = "auto"
	plan := api
	plan.Name = "plan"
	plan.LocalPrefix = "/plan/"
	plan.Billing = "plan"
	unknown := api
	unknown.Name = "unknown"
	unknown.LocalPrefix = "/unknown/"
	unknown.Usage = "off"
	configure(t, s, Config{Routes: []Route{api, plan, unknown}}, map[string]material{"vendor": boundMaterial(t, upstream.URL, "vendor-canary")})
	token := caller(t, s, "api", "plan", "unknown")
	cookie, csrf := loginAdmin(t, s)
	settings := MoneySettings{Timezone: "UTC", Budget: &MoneyBudget{Currency: "USD", LimitMicros: 1000000}, Rates: []MoneyRate{{Provider: "api", Model: "fixture-large", Currency: "USD", Source: "SYNTHETIC PRICE LIST", AsOf: "2026-11-01", Input: moneyPtr(3000000), Output: moneyPtr(15000000), CacheRead: moneyPtr(300000), CacheWrite: moneyPtr(0)}}}
	if rr := adminRequest(s, "PUT", "money", settings, cookie, csrf); rr.Code != 200 {
		t.Fatalf("save settings: %d %s", rr.Code, rr.Body.String())
	}
	for _, prefix := range []string{"/api/", "/plan/", "/unknown/"} {
		req := httptest.NewRequest("POST", prefix+"responses", strings.NewReader(`{"synthetic":true}`))
		req.Header.Set("X-Local", "Local "+token)
		req.Header.Set("X-Term-LLM-Proxy-App", "Kitchen assistant")
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != 200 {
			t.Fatalf("forward: %d %s", rr.Code, rr.Body.String())
		}
	}
	if hits.Load() != 3 {
		t.Fatal("unexpected replay/count")
	}
	check := func() MoneySummary {
		t.Helper()
		rr := adminRequest(s, "GET", "summary", nil, cookie, csrf)
		if rr.Code != 200 {
			t.Fatalf("summary: %d %s", rr.Code, rr.Body.String())
		}
		var summary MoneySummary
		if err := json.Unmarshal(rr.Body.Bytes(), &summary); err != nil {
			t.Fatal(err)
		}
		if summary.Month.Requests != 3 || summary.Month.Included != 1 || summary.Month.Unmeasured != 1 || len(summary.Month.Totals) != 1 || summary.Month.Totals[0].Total != 49950 {
			t.Fatalf("wrong backend-populated summary: %+v", summary)
		}
		if summary.Budget == nil || !summary.Budget.WarningOnly || !summary.Budget.Incomplete || summary.Budget.RemainingMicros != 950050 {
			t.Fatalf("misleading budget: %+v", summary.Budget)
		}
		if len(summary.Apps) != 1 || summary.Apps[0].Name != "Kitchen assistant" {
			t.Fatal("app attribution missing")
		}
		return summary
	}
	check()
	records, _, err := s.store.records(nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if r.Route == "plan" && (r.Money == nil || r.Money.Amount != nil || r.Money.Currency != "") {
			t.Fatal("plan was assigned a charge")
		}
	}
	if _, err := s.store.db.Exec("DELETE FROM requests"); err != nil {
		t.Fatal(err)
	}
	check() // monthly evidence is not the short-lived request table or a page
	// Later price changes cannot silently reprice old activity.
	settings.Rates[0].Input = moneyPtr(9000000)
	if rr := adminRequest(s, "PUT", "money", settings, cookie, csrf); rr.Code != 200 {
		t.Fatal(rr.Code)
	}
	check()
	if rr := adminRequest(s, "GET", "summary", nil, nil, ""); rr.Code != 401 {
		t.Fatal("summary exposed without admin session")
	}
}

func TestMoneyAndRequestTerminalWriteAreAtomicAndIdempotent(t *testing.T) {
	s := testService(t)
	rec := Record{ID: "money-atomic", Started: time.Now().UnixMilli(), Outcome: "unknown"}
	if err := s.store.start(context.Background(), &rec); err != nil {
		t.Fatal(err)
	}
	rec.Outcome = "complete"
	rec.Money = &Money{Billing: "plan", Kind: "included"}
	if _, err := s.store.db.Exec("CREATE TRIGGER fail_money BEFORE INSERT ON money_history BEGIN SELECT RAISE(ABORT,'synthetic'); END"); err != nil {
		t.Fatal(err)
	}
	if err := s.store.finish(rec); err == nil {
		t.Fatal("money persistence failure ignored")
	}
	var outcome string
	s.store.db.QueryRow("SELECT outcome FROM requests WHERE id=?", rec.ID).Scan(&outcome)
	if outcome != "unknown" {
		t.Fatal("request marked complete without money snapshot")
	}
	if _, err := s.store.db.Exec("DROP TRIGGER fail_money"); err != nil {
		t.Fatal(err)
	}
	if err := s.store.finish(rec); err != nil {
		t.Fatal(err)
	}
	if err := s.store.finish(rec); err != nil {
		t.Fatal(err)
	}
	var n int
	s.store.db.QueryRow("SELECT count(*) FROM money_history WHERE request_id=?", rec.ID).Scan(&n)
	if n != 1 {
		t.Fatal("money request counted twice")
	}
}
