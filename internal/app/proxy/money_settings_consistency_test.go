package proxy

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func assertMoneySettingsViews(t *testing.T, s *Service, want MoneySettings) {
	t.Helper()
	cookie, csrf := loginAdmin(t, s)
	rr := adminRequest(s, "GET", "money", nil, cookie, csrf)
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	var got MoneySettings
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("GET money: %+v want %+v err %v", got, want, err)
	}
	rr = adminRequest(s, "GET", "state", nil, cookie, csrf)
	var state struct {
		Money MoneySettings `json:"money"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &state); rr.Code != 200 || err != nil || !reflect.DeepEqual(state.Money, want) {
		t.Fatalf("state disagrees: %d %+v %v", rr.Code, state, err)
	}
	rr = adminRequest(s, "GET", "export", nil, cookie, csrf)
	var exported Config
	if err := yaml.Unmarshal(rr.Body.Bytes(), &exported); rr.Code != 200 || err != nil || !reflect.DeepEqual(exported.Money, want) {
		t.Fatalf("export disagrees: %d %+v %v", rr.Code, exported.Money, err)
	}
	pricing, err := s.currentMoneySettings(context.Background())
	if err != nil || !reflect.DeepEqual(pricing, want) {
		t.Fatalf("pricing disagrees: %+v %v", pricing, err)
	}
}
func TestMoneySettingsConfigImportExportStateAndRestartAgree(t *testing.T) {
	s := testService(t)
	cookie, csrf := loginAdmin(t, s)
	settings := MoneySettings{Timezone: "Australia/Sydney", Budget: &MoneyBudget{Currency: "AUD", LimitMicros: 40000000}, Rates: []MoneyRate{testMoneyRate()}}
	revision := s.snap.revision
	if rr := adminRequest(s, "PUT", "money", settings, cookie, csrf); rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	if s.snap.revision != revision+1 {
		t.Fatal("money edit did not invalidate stale config writers")
	}
	assertMoneySettingsViews(t, s, settings)
	// A browser route-only save must preserve canonical table settings.
	rr := adminRequest(s, "PUT", "config", map[string]any{"revision": s.snap.revision, "routes": []Route{}}, cookie, csrf)
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	assertMoneySettingsViews(t, s, settings)
	settings.Budget.LimitMicros = 50000000
	rr = adminRequest(s, "PUT", "config", map[string]any{"revision": s.snap.revision, "routes": []Route{}, "money": settings}, cookie, csrf)
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	assertMoneySettingsViews(t, s, settings)
	settings.Timezone = "UTC"
	exported, err := yaml.Marshal(Config{Routes: []Route{}, Money: settings})
	if err != nil {
		t.Fatal(err)
	}
	rr = adminRequest(s, "POST", "import", map[string]any{"revision": s.snap.revision, "yaml": string(exported)}, cookie, csrf)
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	assertMoneySettingsViews(t, s, settings)
	opts := s.opts
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	assertMoneySettingsViews(t, next, settings)
	// Export transfers the price evidence that is actually used on a fresh box.
	other := testService(t)
	otherCookie, otherCSRF := loginAdmin(t, other)
	rr = adminRequest(other, "POST", "import", map[string]any{"revision": other.snap.revision, "yaml": string(exported)}, otherCookie, otherCSRF)
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	assertMoneySettingsViews(t, other, settings)
}
func TestMoneySettingsAndConfigurationRollBackTogether(t *testing.T) {
	s := testService(t)
	cookie, csrf := loginAdmin(t, s)
	before, err := s.currentMoneySettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	revision := s.snap.revision
	var configBefore string
	if err = s.store.db.QueryRow("SELECT data FROM configuration WHERE id=1").Scan(&configBefore); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.db.Exec("CREATE TRIGGER reject_settings BEFORE UPDATE ON money_settings BEGIN SELECT RAISE(ABORT,'synthetic'); END"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"money", "config", "import"} {
		t.Run(path, func(t *testing.T) {
			settings := MoneySettings{Timezone: "UTC", Budget: &MoneyBudget{Currency: "USD", LimitMicros: 123}}
			method := "PUT"
			var body any = settings
			if path == "config" {
				body = map[string]any{"revision": revision, "routes": []Route{}, "money": settings}
			}
			if path == "import" {
				method = "POST"
				raw, err := yaml.Marshal(Config{Routes: []Route{}, Money: settings})
				if err != nil {
					t.Fatal(err)
				}
				body = map[string]any{"revision": revision, "yaml": string(raw)}
			}
			rr := adminRequest(s, method, path, body, cookie, csrf)
			if rr.Code != 503 {
				t.Fatalf("failed settings commit reported success: %d %s", rr.Code, rr.Body.String())
			}
			var configAfter string
			var storedRevision int64
			if err := s.store.db.QueryRow("SELECT revision,data FROM configuration WHERE id=1").Scan(&storedRevision, &configAfter); err != nil {
				t.Fatal(err)
			}
			if configBefore != configAfter || storedRevision != revision || s.snap.revision != revision {
				t.Fatal("config changed despite money rollback")
			}
			assertMoneySettingsViews(t, s, before)
		})
	}
}
func TestMoneySettingsCanonicalTableOverridesLegacyStaleMirror(t *testing.T) {
	s := testService(t)
	canonical := MoneySettings{Timezone: "UTC", Budget: &MoneyBudget{Currency: "EUR", LimitMicros: 100}}
	raw, err := json.Marshal(canonical)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the pre-fix split store. Readers must never export the stale mirror.
	if _, err = s.store.db.Exec("UPDATE money_settings SET data=? WHERE id=1", string(raw)); err != nil {
		t.Fatal(err)
	}
	assertMoneySettingsViews(t, s, canonical)
	// File mode is deliberately a separate authority and never reads UI DB prices.
	s.opts.ConfigFile = "operator-managed"
	s.snap.config.Money = MoneySettings{Timezone: "Australia/Sydney"}
	assertMoneySettingsViews(t, s, s.snap.config.Money)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/_proxy/api/money", nil)
	s.moneyAPI(rr, req, "money")
	if rr.Code != 409 {
		t.Fatal("file mode accepted money write", rr.Code)
	}
}
