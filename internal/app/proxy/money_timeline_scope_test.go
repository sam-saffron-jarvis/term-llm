package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/app/proxy/moneytimeline"
	"github.com/term-llm/term-llm/internal/app/proxy/usagetap"
)

func TestTimelineAPIScopeIntersection(t *testing.T) {
	s := testService(t)
	cookie, csrf := loginAdmin(t, s)
	now := time.Now().UTC()
	// Coverage is based on all retained history, not the first matching row.
	insertTimelineEvidence(t, s, "oldest", now.AddDate(0, 0, -15), timelineCharge("GBP", 1000))
	const tag, provider, model = " release+%;&/ ", " OpenAI ", "Model+V1 "
	for _, window := range []struct {
		id       string
		daysAgo  int
		currency string
		charged  int64
	}{
		{"current", 2, "USD", 80},
		{"previous", 9, "EUR", 150},
	} {
		at := now.AddDate(0, 0, -window.daysAgo)
		paid := timelineCharge(window.currency, window.charged)
		paid.DeviceID, paid.Device, paid.Tag, paid.Provider = "device", "Device", tag, provider
		paid.Usage = &ObservedUsage{Protocol: usagetap.Responses, Usage: usagetap.Usage{
			Model: model, Input: 10, Output: 2, Source: usagetap.Reported,
		}}
		plan := paid
		plan.Usage = nil
		plan.Money = timelinePlanEstimate("estimated_floor", window.currency, 30)
		plan.Money.APIEquivalent.Model = model
		insertTimelineEvidence(t, s, window.id+"-paid", at, paid)
		insertTimelineEvidence(t, s, window.id+"-plan", at, plan)

		// Each decoy matches two scopes but would pollute a different part of the result.
		otherTag, otherProvider, otherModel := paid, paid, paid
		otherTag.Tag = ""
		otherTag.Money = timelineCharge("GBP", 9000).Money
		otherProvider.Provider = "openai" // Similar name, different key.
		otherProvider.Money = timelinePlanEstimate("estimated", window.currency, 9000)
		otherModel.Usage = &ObservedUsage{Protocol: usagetap.Responses, Usage: usagetap.Usage{Model: "Model+V1"}}
		otherModel.Money = Money{Billing: "pay_as_you_go", Kind: "unmeasured"}
		insertTimelineEvidence(t, s, window.id+"-other-tag", at, otherTag)
		insertTimelineEvidence(t, s, window.id+"-other-provider", at, otherProvider)
		insertTimelineEvidence(t, s, window.id+"-other-model", at, otherModel)
	}

	q := url.Values{
		"range": {"7d"}, "timezone": {"UTC"}, "group": {"provider"},
		"scope_client": {"device\x00" + tag}, "scope_provider": {provider}, "scope_model": {"model:" + model},
	}
	got := decodeTimelineScopeResponse(t, adminRequest(s, http.MethodGet, "timeline?"+q.Encode(), nil, cookie, csrf))
	wantCurrent := moneytimeline.Series{
		Key: provider, Label: "OpenAI", Tokens: moneyPtr(12), TokenParts: &moneytimeline.TokenParts{Input: 10, Output: 2},
		UnknownCost: 1, UnknownTokens: 1, Plan: 1,
		Costs: []moneytimeline.Cost{{Currency: "USD", Micros: 110, Estimated: true, PlanEstimateMicros: moneyPtr(30)}},
	}
	wantPrevious := wantCurrent
	wantPrevious.Key, wantPrevious.Label = "total", "Total"
	wantPrevious.Costs = []moneytimeline.Cost{{Currency: "EUR", Micros: 180, Estimated: true, PlanEstimateMicros: moneyPtr(30)}}
	assertTimelineScopeResponse(t, got, []string{"USD"}, []moneytimeline.Series{wantCurrent}, wantPrevious)

	// With only the model scoped, group by client and keep both matching series.
	q.Set("group", "client")
	q.Del("scope_client")
	q.Del("scope_provider")
	got = decodeTimelineScopeResponse(t, adminRequest(s, http.MethodGet, "timeline?"+q.Encode(), nil, cookie, csrf))
	untagged := moneytimeline.Series{
		Key: "device", Label: "Device", Tokens: moneyPtr(12), TokenParts: &moneytimeline.TokenParts{Input: 10, Output: 2},
		Costs: []moneytimeline.Cost{{Currency: "GBP", Micros: 9000}},
	}
	tagged := wantCurrent
	tagged.Key, tagged.Label = "device\x00"+tag, "Device · "+tag
	tagged.Tokens, tagged.TokenParts = moneyPtr(24), &moneytimeline.TokenParts{Input: 20, Output: 4}
	tagged.Plan = 2
	tagged.Costs = []moneytimeline.Cost{{Currency: "USD", Micros: 9110, Estimated: true, PlanEstimateMicros: moneyPtr(9030)}}
	wantPrevious = tagged
	wantPrevious.Key, wantPrevious.Label = "total", "Total"
	wantPrevious.Tokens, wantPrevious.TokenParts = moneyPtr(36), &moneytimeline.TokenParts{Input: 30, Output: 6}
	wantPrevious.Costs = []moneytimeline.Cost{
		{Currency: "EUR", Micros: 9180, Estimated: true, PlanEstimateMicros: moneyPtr(9030)},
		{Currency: "GBP", Micros: 9000},
	}
	assertTimelineScopeResponse(t, got, []string{"GBP", "USD"}, []moneytimeline.Series{untagged, tagged}, wantPrevious)
}

func TestTimelineAPIScopeUnknownIdentities(t *testing.T) {
	s := testService(t)
	cookie, csrf := loginAdmin(t, s)
	now := time.Now().UTC()
	unknown := timelineCharge("USD", 5)
	otherClient, otherProvider, otherModel := unknown, unknown, unknown
	otherClient.DeviceID = "known-client"
	otherProvider.Provider = "known-provider"
	otherModel.Usage = &ObservedUsage{Usage: usagetap.Usage{Model: "known-model"}}
	for _, daysAgo := range []int{2, 9} {
		at := now.AddDate(0, 0, -daysAgo)
		id := at.Format(time.DateOnly)
		insertTimelineEvidence(t, s, id+"-unknown", at, unknown)
		insertTimelineEvidence(t, s, id+"-client", at, otherClient)
		insertTimelineEvidence(t, s, id+"-provider", at, otherProvider)
		insertTimelineEvidence(t, s, id+"-model", at, otherModel)
	}
	insertTimelineEvidence(t, s, "oldest", now.AddDate(0, 0, -15), otherClient)
	const path = "timeline?range=7d&timezone=UTC&group=client&scope_client=&scope_provider=&scope_model="
	got := decodeTimelineScopeResponse(t, adminRequest(s, http.MethodGet, path+"model%3A", nil, cookie, csrf))
	want := moneytimeline.Series{Key: "", Label: "Unknown client", Costs: []moneytimeline.Cost{{Currency: "USD", Micros: 5}}, UnknownTokens: 1}
	wantPrevious := want
	wantPrevious.Key, wantPrevious.Label = "total", "Total"
	assertTimelineScopeResponse(t, got, []string{"USD"}, []moneytimeline.Series{want}, wantPrevious)

	// An empty model value is also exact: it is not the emitted unknown-model key.
	got = decodeTimelineScopeResponse(t, adminRequest(s, http.MethodGet, path, nil, cookie, csrf))
	assertTimelineScopeResponse(t, got, []string{}, nil, moneytimeline.Series{Key: "total", Label: "Total", Costs: []moneytimeline.Cost{}})
}

func TestTimelineAPIScopeRejectsMalformedQuery(t *testing.T) {
	s := testService(t)
	cookie, csrf := loginAdmin(t, s)
	for _, query := range []string{
		"scope_client=&scope_client=another",
		"scope_provider=one&scope_provider=one",
		"scope_model=model%3A&scope_%6dodel=model%3A",
		"scope_client=device%ZZ",
	} {
		t.Run(query, func(t *testing.T) {
			w := adminRequest(s, http.MethodGet, "timeline?"+query, nil, cookie, csrf)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", w.Code, w.Body.String())
			}
		})
	}
}

func decodeTimelineScopeResponse(t *testing.T, w *httptest.ResponseRecorder) moneytimeline.Response {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("timeline status %d: %s", w.Code, w.Body.String())
	}
	var got moneytimeline.Response
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func assertTimelineScopeResponse(t *testing.T, got moneytimeline.Response, currencies []string, current []moneytimeline.Series, previous moneytimeline.Series) {
	t.Helper()
	var series []moneytimeline.Series
	for _, bucket := range got.Buckets {
		series = append(series, bucket.Series...)
	}
	if !reflect.DeepEqual(got.Currencies, currencies) {
		t.Errorf("currencies = %v, want %v", got.Currencies, currencies)
	}
	if !reflect.DeepEqual(series, current) {
		t.Errorf("current series = %+v, want %+v", series, current)
	}
	if !reflect.DeepEqual(got.Previous, &previous) {
		t.Errorf("previous series = %+v, want %+v", got.Previous, previous)
	}
}
