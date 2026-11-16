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
)

func moneySummaryNow(t *testing.T, s *Service) MoneySummary {
	t.Helper()
	result, err := s.moneySummary(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestMoneyAdmissionPlaceholderAndFailedFinishRemainIncomplete(t *testing.T) {
	s := testService(t)
	for _, billing := range []string{"pay_as_you_go", "plan"} {
		rec := Record{ID: "pending-" + billing, Started: time.Now().UnixMilli(), CallerID: "device", CallerName: "Laptop", Route: "provider", App: "Kitchen", Money: pendingRequestMoney(billing)}
		if err := s.store.start(context.Background(), &rec); err != nil {
			t.Fatal(err)
		}
		var raw string
		if err := s.store.db.QueryRow("SELECT data FROM money_history WHERE request_id=?", rec.ID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var evidence MoneyRecord
		if err := json.Unmarshal([]byte(raw), &evidence); err != nil {
			t.Fatal(err)
		}
		if evidence.Money.Billing != billing || evidence.Money.Kind != "unmeasured" || evidence.Money.Amount != nil || evidence.DeviceID != "device" || evidence.Device != "Laptop" || evidence.App != "Kitchen" || evidence.Provider != "provider" {
			t.Fatalf("admission attribution missing: %+v", evidence)
		}
	}
	before := moneySummaryNow(t, s)
	if before.Month.Requests != 2 || before.Month.Unmeasured != 1 || before.Month.PlanUnmeasured != 1 || len(before.Month.Totals) != 0 {
		t.Fatalf("in-flight use vanished: %+v", before.Month)
	}
	if _, err := s.store.db.Exec("CREATE TRIGGER reject_terminal BEFORE UPDATE ON requests BEGIN SELECT RAISE(ABORT,'synthetic'); END"); err != nil {
		t.Fatal(err)
	}
	rec := Record{ID: "pending-pay_as_you_go", Started: time.Now().UnixMilli(), Outcome: "complete", Money: &Money{Billing: "pay_as_you_go", Kind: "charged", Amount: moneyPtr(123), Currency: "USD", Source: "fixture", AsOf: "2026-11-16"}}
	if err := s.store.finish(rec); err == nil {
		t.Fatal("terminal error ignored")
	}
	after := moneySummaryNow(t, s)
	if after.Month.Unmeasured != 1 || after.Month.PlanUnmeasured != 1 || len(after.Month.Totals) != 0 {
		t.Fatal("failed terminal persistence lost unknown evidence", after.Month)
	}
}
func TestMoneyAdmissionFailureRollsBackRequestAndEvidence(t *testing.T) {
	s := testService(t)
	if _, err := s.store.db.Exec("CREATE TRIGGER reject_admission_money BEFORE INSERT ON money_history BEGIN SELECT RAISE(ABORT,'synthetic'); END"); err != nil {
		t.Fatal(err)
	}
	rec := Record{ID: "blocked-start", Started: time.Now().UnixMilli(), Money: pendingRequestMoney("pay_as_you_go")}
	if err := s.store.start(context.Background(), &rec); err == nil {
		t.Fatal("admitted without durable placeholder")
	}
	for _, table := range []string{"requests", "money_history"} {
		var count int
		if err := s.store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("admission partially committed: %s %d %v", table, count, err)
		}
	}
}
func TestMoneyOrphanRecoveryOnRestartIsIdempotent(t *testing.T) {
	s := testService(t)
	rec := Record{ID: "pre-fix-orphan", Started: time.Now().UnixMilli(), CallerID: "legacy-device", CallerName: "Legacy laptop", Route: "legacy", App: "chat", Outcome: "unknown", Money: pendingRequestMoney("plan")}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.db.Exec("INSERT INTO requests(id,started,caller,route,method,path,outcome,data) VALUES(?,?,?,?,?,?,?,?)", rec.ID, rec.Started, rec.CallerID, rec.Route, "POST", "/responses", "unknown", string(raw)); err != nil {
		t.Fatal(err)
	}
	opts := s.opts
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	for i := 0; i < 2; i++ {
		result := moneySummaryNow(t, next)
		if result.Month.Requests != 1 || result.Month.PlanUnmeasured != 1 || result.Month.Included != 0 {
			t.Fatalf("orphan was lost/double counted: %+v", result.Month)
		}
	}
	var count int
	if err = next.store.db.QueryRow("SELECT count(*) FROM money_history WHERE request_id=?", rec.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
func TestMoneyRealInFlightTrafficIsVisibleBeforeFinish(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; fmt.Fprint(w, "ok") }))
	defer upstream.Close()
	s := testService(t)
	r := route("paid", "/paid/", upstream.URL, "X-Local", "key")
	r.Billing = "pay_as_you_go"
	configure(t, s, Config{Routes: []Route{r}}, map[string]material{"key": boundMaterial(t, upstream.URL, "secret")})
	token := caller(t, s, r.Name)
	done := make(chan struct{})
	go func() {
		defer close(done)
		call(s, "POST", "/paid/responses", "X-Local", token, strings.NewReader("{}"))
	}()
	<-entered
	// Ensure an early assertion failure cannot strand the forwarding goroutine.
	defer func() { close(release); <-done }()
	result := moneySummaryNow(t, s)
	if result.Month.Requests != 1 || result.Month.Unmeasured+result.Month.Unspecified != 1 || len(result.Month.Totals) != 0 {
		t.Fatalf("actual in-flight request hidden: %+v", result.Month)
	}
}
func TestMoneySummaryStreamsBusyMonthAndPreservesCorruptCoverage(t *testing.T) {
	s := testService(t)
	now := time.Now()
	row := MoneyRecord{Started: now, DeviceID: "device", Device: "Laptop", App: "chat", Provider: "api", Money: Money{Billing: "pay_as_you_go", Kind: "charged", Amount: moneyPtr(1), Currency: "USD", Source: "fixture", AsOf: "2026-11-16"}}
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<50002)
 INSERT INTO money_history(request_id,started,caller,route,data) SELECT 'busy-'||x,?,'device','api',? FROM n`, now.UnixMilli(), string(raw)); err != nil {
		t.Fatal(err)
	}
	for i, data := range []string{"not json", "null", "{}"} {
		if _, err = s.store.db.Exec("INSERT INTO money_history(request_id,started,caller,route,data) VALUES(?,?,?,?,?)", fmt.Sprintf("corrupt-%d", i), now.UnixMilli(), "device", "api", data); err != nil {
			t.Fatal(err)
		}
	}
	result := moneySummaryNow(t, s)
	if result.Month.Requests != 50005 || result.Month.Unmeasured != 3 || len(result.Month.Totals) != 1 || result.Month.Totals[0].Total != 50002 || result.Month.Totals[0].Qualifier != "at_least" {
		t.Fatalf("busy/corrupt month was lost: %+v", result.Month)
	}
	if len(result.Devices) != 1 || len(result.Apps) != 2 || len(result.Providers) != 1 {
		t.Fatalf("streamed attribution lost: %+v", result)
	}
}

func TestMoneyMigrationPreservesCorruptLegacyRowAsUnknown(t *testing.T) {
	s := testService(t)
	now := time.Now().UnixMilli()
	if _, err := s.store.db.Exec("INSERT INTO requests(id,started,caller,route,method,path,outcome,data) VALUES('corrupt-legacy',?,'device','route','POST','/','unknown','not JSON')", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec("DROP TABLE connect_requests; DROP TABLE connect_managed; DROP TABLE accounts; DROP TABLE money_history; DROP TABLE money_settings; PRAGMA user_version=2"); err != nil {
		t.Fatal(err)
	}
	if err := s.store.initialize(); err != nil {
		t.Fatal("corrupt legacy metadata blocked migration", err)
	}
	result := moneySummaryNow(t, s)
	if result.Month.Requests != 1 || result.Month.Unmeasured != 1 || len(result.Month.Totals) != 0 {
		t.Fatalf("corrupt legacy row disappeared: %+v", result.Month)
	}
}
