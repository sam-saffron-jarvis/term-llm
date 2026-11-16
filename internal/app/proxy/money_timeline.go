package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

const timelineOtherKey = "\x00timeline-other"

type moneyTimeline struct {
	Timezone   string           `json:"timezone"`
	Range      string           `json:"range"`
	Start      time.Time        `json:"start"`
	End        time.Time        `json:"end"`
	Currencies []string         `json:"currencies"`
	Buckets    []timelineBucket `json:"buckets"`
}
type timelineBucket struct {
	Start  time.Time        `json:"start"`
	End    time.Time        `json:"end"`
	Label  string           `json:"label"`
	Series []timelineSeries `json:"series"`
}
type timelineSeries struct {
	Key           string         `json:"key"`
	Label         string         `json:"label"`
	Costs         []timelineCost `json:"costs"`
	Tokens        *int64         `json:"tokens,omitempty"`
	UnknownCost   int64          `json:"unknown_cost"`
	UnknownTokens int64          `json:"unknown_tokens"`
	Plan          int64          `json:"plan"`
}
type timelineCost struct {
	Currency  string `json:"currency"`
	Micros    int64  `json:"micros"`
	Estimated bool   `json:"estimated"`
}
type timelineAggregate struct {
	label                            string
	cost                             map[string]int64
	estimated                        map[string]bool
	tokens                           int64
	tokenKnown                       bool
	unknownCost, unknownTokens, plan int64
	requests                         int64
}

func (s *Service) moneyTimeline(ctx context.Context, now time.Time, period, group string) (moneyTimeline, error) {
	settings, err := s.currentMoneySettings(ctx)
	if err != nil {
		return moneyTimeline{}, err
	}
	zone := settings.Timezone
	if zone == "" {
		zone = "UTC"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return moneyTimeline{}, err
	}
	start, bounds, hourly, err := timelineBounds(now, loc, period)
	if err != nil {
		return moneyTimeline{}, err
	}
	count := len(bounds) - 1
	if group != "total" && group != "client" && group != "provider" {
		return moneyTimeline{}, fmt.Errorf("invalid group")
	}
	end := now
	result := moneyTimeline{Timezone: zone, Range: period, Start: start, End: end, Currencies: []string{}, Buckets: make([]timelineBucket, count)}
	for i := 0; i < count; i++ {
		e := bounds[i+1]
		if e.After(now) {
			e = now
		}
		if e.Before(bounds[i]) {
			e = bounds[i]
		}
		label := bounds[i].In(loc).Format("2006-01-02 15:00 -0700")
		if !hourly {
			label = bounds[i].In(loc).Format("2006-01-02")
		}
		result.Buckets[i] = timelineBucket{Start: bounds[i], End: e, Label: label, Series: []timelineSeries{}}
	}
	// Repair retained rows before reading so pre-feature requests are represented
	// and missing response observations can be backfilled without repricing.
	if err := s.store.reconcileMoneyHistory(ctx, start.UnixMilli(), now.UnixMilli()); err != nil {
		return moneyTimeline{}, err
	}
	// Stream every retained matching financial row. No LIMIT or request-body data.
	rows, err := s.store.db.QueryContext(ctx, "SELECT started,caller,route,data FROM money_history WHERE started>=? AND started<=? ORDER BY started,request_id", start.UnixMilli(), now.UnixMilli())
	if err != nil {
		return moneyTimeline{}, err
	}
	defer rows.Close()
	aggs := make([]map[string]*timelineAggregate, count)
	currencySet := map[string]bool{}
	for i := range aggs {
		aggs[i] = map[string]*timelineAggregate{}
	}
	for rows.Next() {
		var ms int64
		var caller, route, data string
		if err = rows.Scan(&ms, &caller, &route, &data); err != nil {
			return moneyTimeline{}, err
		}
		var rec MoneyRecord
		if json.Unmarshal([]byte(data), &rec) != nil {
			rec.Money = *pendingRequestMoney("unspecified")
		}
		t := time.UnixMilli(ms)
		idx := timelineBucketIndex(t, bounds)
		if idx < 0 {
			continue
		}
		key, label := timelineGroupIdentity(rec, caller, route, group)
		a := aggs[idx][key]
		if a == nil {
			a = &timelineAggregate{label: label, cost: map[string]int64{}, estimated: map[string]bool{}}
			aggs[idx][key] = a
		}
		accumulateTimelineRecord(a, rec, currencySet)
	}
	if err = rows.Err(); err != nil {
		return moneyTimeline{}, err
	}
	for c := range currencySet {
		result.Currencies = append(result.Currencies, c)
	}
	sort.Strings(result.Currencies)
	finalizeTimelineBuckets(&result, aggs)
	return result, nil
}

func timelineProviderLabel(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "openai":
		return "OpenAI"
	case "anthropic", "claude-code", "claude-bin":
		return "Claude"
	case "chatgpt":
		return "ChatGPT"
	case "grok", "xai", "grok-bin":
		return "Grok"
	case "gemini", "google":
		return "Gemini"
	case "copilot":
		return "Copilot"
	default:
		return ""
	}
}

func mergeTimelineAggregate(dst, src *timelineAggregate) {
	for currency, value := range src.cost {
		if value > maxMoneyMicros-dst.cost[currency] {
			dst.unknownCost++
			dst.estimated[currency] = true
		} else {
			dst.cost[currency] += value
		}
		dst.estimated[currency] = dst.estimated[currency] || src.estimated[currency]
	}
	dst.unknownCost += src.unknownCost
	dst.unknownTokens += src.unknownTokens
	dst.plan += src.plan
	dst.requests += src.requests
	if dst.tokens > maxMoneyMicros-src.tokens {
		dst.unknownTokens++
	} else {
		dst.tokens += src.tokens
	}
	dst.tokenKnown = dst.tokenKnown || src.tokenKnown
}

func (s *Service) timelineAPI(w http.ResponseWriter, r *http.Request) bool {
	period := r.URL.Query().Get("range")
	if period == "" {
		period = "30d"
	}
	group := r.URL.Query().Get("group")
	if group == "" {
		group = "total"
	}
	if (period != "today" && period != "7d" && period != "30d") || (group != "total" && group != "client" && group != "provider") {
		fail(w, 400, "Invalid timeline range or grouping")
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out, err := s.moneyTimeline(ctx, time.Now(), period, group)
	if err != nil {
		fail(w, 503, "Timeline unavailable; no missing usage has been assumed to be zero")
		return true
	}
	jsonReply(w, 200, out)
	return true
}

var _ = http.MethodGet
