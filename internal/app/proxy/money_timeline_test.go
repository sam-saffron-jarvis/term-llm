package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/app/proxy/usagetap"
)

func setTimelineZone(t *testing.T, s *Service, zone string) {
	t.Helper()
	raw, _ := json.Marshal(MoneySettings{Timezone: zone})
	if _, err := s.store.db.Exec("UPDATE money_settings SET data=? WHERE id=1", string(raw)); err != nil {
		t.Fatal(err)
	}
}
func TestMoneyTimelineBinsUseLocalCalendar(t *testing.T) {
	s := testService(t)
	setTimelineZone(t, s, "Australia/Sydney")
	now := time.Date(2026, 11, 17, 12, 30, 0, 0, time.UTC)
	got, err := s.moneyTimeline(context.Background(), now, "7d", "total")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Buckets) != 7 || got.Timezone != "Australia/Sydney" || got.Buckets[0].Start.In(time.FixedZone("", 11*3600)).Day() != 11 {
		t.Fatalf("unexpected bounds: %+v", got)
	}
	if got.Buckets[6].End.After(now) || got.Buckets[6].End.Before(got.Buckets[6].Start) {
		t.Fatalf("invalid/future bucket: %+v", got.Buckets[6])
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if json.Unmarshal(data, &wire) != nil || wire["buckets"] == nil {
		t.Fatal("invalid timeline schema")
	}
}
func TestMoneyTimelineTodayDSTRepeatHasDistinctOffset(t *testing.T) {
	s := testService(t)
	setTimelineZone(t, s, "America/New_York")
	now := time.Date(2026, 11, 2, 4, 59, 0, 0, time.UTC)
	got, err := s.moneyTimeline(context.Background(), now, "today", "total")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Buckets) != 25 {
		t.Fatalf("fall-back bins=%d", len(got.Buckets))
	}
	labels := map[string]bool{}
	for _, b := range got.Buckets {
		labels[b.Label] = true
		if b.End.Before(b.Start) {
			t.Fatalf("inverted bucket: %+v", b)
		}
	}
	if len(labels) != 25 {
		t.Fatalf("repeated hour labels collide: %v", labels)
	}
}
func TestMoneyTimelineKeepsKnownTokensBesideUnknownEvidence(t *testing.T) {
	s := testService(t)
	now := time.Date(2026, 11, 17, 12, 30, 0, 0, time.UTC)
	rows := []struct {
		id    string
		usage *ObservedUsage
	}{{"known", &ObservedUsage{Protocol: usagetap.Responses, Usage: usagetap.Usage{Input: 10, CachedInput: 2, Output: 4, Reasoning: 1, Source: usagetap.Reported}}}, {"partial", &ObservedUsage{Protocol: usagetap.Messages, Usage: usagetap.Usage{Input: 20, Output: -1, Source: usagetap.Partial}}}, {"legacy", nil}}
	for _, row := range rows {
		data, err := json.Marshal(MoneyRecord{Started: now, Provider: "p", Money: Money{Billing: "pay_as_you_go", Kind: "unmeasured"}, Usage: row.usage})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.store.db.Exec("INSERT INTO money_history(request_id,started,caller,route,data) VALUES(?,?,?,?,?)", row.id, now.UnixMilli(), "client", "route", string(data)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.moneyTimeline(context.Background(), now, "today", "total")
	if err != nil {
		t.Fatal(err)
	}
	var found *timelineSeries
	for i := range got.Buckets {
		if len(got.Buckets[i].Series) > 0 {
			found = &got.Buckets[i].Series[0]
			break
		}
	}
	if found == nil || found.Tokens == nil || *found.Tokens != 14 || found.UnknownTokens != 2 {
		t.Fatalf("known tokens lost: %+v", found)
	}
}
func TestMoneyTimelineGroupsUnknownOnceAndCapsSeries(t *testing.T) {
	s := testService(t)
	now := time.Date(2026, 11, 17, 12, 30, 0, 0, time.UTC)
	for i := 0; i < 8; i++ {
		data, _ := json.Marshal(MoneyRecord{Started: now, DeviceID: fmt.Sprintf("client-%d", i), Device: fmt.Sprintf("Client %d", i), Provider: "openai", Money: Money{Billing: "pay_as_you_go", Kind: "unmeasured"}})
		if _, err := s.store.db.Exec("INSERT INTO money_history(request_id,started,caller,route,data) VALUES(?,?,?,?,?)", fmt.Sprintf("row-%d", i), now.UnixMilli(), fmt.Sprintf("client-%d", i), "openai", string(data)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.moneyTimeline(context.Background(), now, "7d", "client")
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, b := range got.Buckets {
		for _, item := range b.Series {
			if item.UnknownCost != 0 && item.Key != timelineOtherKey && item.UnknownCost != 1 {
				t.Fatalf("unknown cost counted incorrectly: %+v", item)
			}
			total += item.UnknownCost
		}
		if len(b.Series) > 6 {
			t.Fatalf("too many series: %d", len(b.Series))
		}
	}
	if total != 8 {
		t.Fatalf("unknown requests counted %d, want 8", total)
	}
}
func TestTimelineMergeOverflowRemainsUnknownWithoutSaturation(t *testing.T) {
	dst := &timelineAggregate{cost: map[string]int64{"USD": maxMoneyMicros - 2}, estimated: map[string]bool{}}
	src := &timelineAggregate{cost: map[string]int64{"USD": 10}, estimated: map[string]bool{}}
	mergeTimelineAggregate(dst, src)
	if dst.cost["USD"] != maxMoneyMicros-2 || dst.unknownCost != 1 {
		t.Fatalf("overflow invented exact total: %+v", dst)
	}
}
func TestTimelineOtherIdentityDoesNotReplaceRealOther(t *testing.T) {
	aggs := map[string]*timelineAggregate{"other": {label: "Other route", requests: 10, cost: map[string]int64{}, estimated: map[string]bool{}}, "a": {label: "A", requests: 1, cost: map[string]int64{}, estimated: map[string]bool{}}, "b": {label: "B", requests: 1, cost: map[string]int64{}, estimated: map[string]bool{}}, "c": {label: "C", requests: 1, cost: map[string]int64{}, estimated: map[string]bool{}}, "d": {label: "D", requests: 1, cost: map[string]int64{}, estimated: map[string]bool{}}, "e": {label: "E", requests: 1, cost: map[string]int64{}, estimated: map[string]bool{}}}
	keep := timelineTopKeys([]map[string]*timelineAggregate{aggs})
	series := timelineBucketSeries(aggs, keep, nil)
	if len(series) != 6 {
		t.Fatalf("series=%d", len(series))
	}
	actual, other := false, false
	for _, item := range series {
		if item.Key == "other" && item.Label == "Other route" {
			actual = true
		}
		if item.Key == timelineOtherKey && item.Label == "Other" {
			other = true
		}
	}
	if !actual || !other {
		t.Fatalf("synthetic Other replaced identity: %+v", series)
	}
}
func TestTimelineUnsupportedUsageProtocolIsUnknown(t *testing.T) {
	a := &timelineAggregate{cost: map[string]int64{}, estimated: map[string]bool{}}
	accumulateTimelineRecord(a, MoneyRecord{Usage: &ObservedUsage{Protocol: usagetap.Inapplicable, Usage: usagetap.Usage{Input: 42, Output: 7, Source: usagetap.Reported}}}, map[string]bool{})
	if a.tokenKnown || a.unknownTokens != 1 {
		t.Fatalf("unsupported protocol counted: %+v", a)
	}
}
