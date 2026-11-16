package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServerAdmissionUsesActiveBillingBeforeMoneyFinish(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; io.WriteString(w, "ok") }))
	defer up.Close()
	s := testService(t)
	r := route("active-plan", "/active/", up.URL, "X-Local", "key")
	r.Billing = "plan"
	configure(t, s, Config{Routes: []Route{r}}, map[string]material{"key": boundMaterial(t, up.URL, "synthetic")})
	token := caller(t, s, r.Name)
	// Simulate file-mode's intentionally non-authoritative stored config. The
	// active snapshot, not a lookup of that JSON, must supply admission billing.
	if _, err := s.store.db.Exec(`UPDATE configuration SET data='{"routes":[]}' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); call(s, "POST", "/active/responses", "X-Local", token, nil) }()
	<-entered
	defer func() { close(release); <-done }()
	var raw string
	if err := s.store.db.QueryRow("SELECT data FROM money_history").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var evidence MoneyRecord
	if err := json.Unmarshal([]byte(raw), &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.Money.Billing != "plan" || evidence.Money.Kind != "unmeasured" || evidence.Money.Amount != nil {
		t.Fatal("admission lost active billing", evidence.Money)
	}
}

func TestServerOpenRecoversOldOrphanBeforeTechnicalRetention(t *testing.T) {
	s := testService(t)
	rec := Record{ID: "pre-hook-40-day-orphan", Started: time.Now().AddDate(0, 0, -40).UnixMilli(), CallerID: "old-device", Route: "old-route", Outcome: "unknown", Money: pendingRequestMoney("pay_as_you_go")}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.db.Exec("INSERT INTO requests(id,started,caller,route,method,path,outcome,data) VALUES(?,?,?,?,?,?,?,?)", rec.ID, rec.Started, rec.CallerID, rec.Route, "POST", "/responses", "unknown", string(raw)); err != nil {
		t.Fatal(err)
	}
	opts := s.opts
	s.Close()
	next, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	var count int
	if err = next.store.db.QueryRow("SELECT count(*) FROM money_history WHERE request_id=?", rec.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("Open failed to recover old orphan", count, err)
	}
	if err = next.store.retain(context.Background(), 30); err != nil {
		t.Fatal(err)
	}
	if err = next.store.db.QueryRow("SELECT count(*) FROM requests WHERE id=?", rec.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("technical retention did not run", count, err)
	}
	if err = next.store.db.QueryRow("SELECT count(*) FROM money_history WHERE request_id=?", rec.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("orphan cost coverage vanished during retention", count, err)
	}
}
