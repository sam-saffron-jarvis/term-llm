package proxy

import "sort"

type timelineRank struct {
	key      string
	requests int64
}

func finalizeTimelineBuckets(result *moneyTimeline, aggs []map[string]*timelineAggregate) {
	keep := timelineTopKeys(aggs)
	for i := range result.Buckets {
		result.Buckets[i].Series = timelineBucketSeries(aggs[i], keep, result.Currencies)
	}
}

func timelineTopKeys(aggs []map[string]*timelineAggregate) map[string]bool {
	scores := map[string]int64{}
	for _, bucket := range aggs {
		for key, a := range bucket {
			scores[key] += a.requests
		}
	}
	ranked := make([]timelineRank, 0, len(scores))
	for key, n := range scores {
		ranked = append(ranked, timelineRank{key, n})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].requests == ranked[j].requests {
			return ranked[i].key < ranked[j].key
		}
		return ranked[i].requests > ranked[j].requests
	})
	keep := map[string]bool{}
	limit := len(ranked)
	if limit > 5 {
		limit = 5
	}
	for _, item := range ranked[:limit] {
		keep[item.key] = true
	}
	return keep
}

func timelineBucketSeries(aggregates map[string]*timelineAggregate, keep map[string]bool, currencies []string) []timelineSeries {
	if len(keep) > 0 {
		mergeTimelineOther(aggregates, keep)
	}
	keys := make([]string, 0, len(aggregates))
	for key := range aggregates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	series := make([]timelineSeries, 0, len(keys))
	for _, key := range keys {
		series = append(series, timelineSeriesFromAggregate(key, aggregates[key], currencies))
	}
	return series
}

func mergeTimelineOther(aggregates map[string]*timelineAggregate, keep map[string]bool) {
	other := &timelineAggregate{label: "Other", cost: map[string]int64{}, estimated: map[string]bool{}}
	hasOther := false
	for key, a := range aggregates {
		if keep[key] {
			continue
		}
		hasOther = true
		mergeTimelineAggregate(other, a)
		delete(aggregates, key)
	}
	if hasOther {
		aggregates[timelineOtherKey] = other
	}
}

func timelineSeriesFromAggregate(key string, a *timelineAggregate, currencies []string) timelineSeries {
	label := a.label
	if key == "total" {
		label = "Total"
	}
	if key == timelineOtherKey {
		label = "Other"
	}
	series := timelineSeries{Key: key, Label: label, Costs: []timelineCost{}, UnknownCost: a.unknownCost, UnknownTokens: a.unknownTokens, Plan: a.plan}
	if a.tokenKnown {
		n := a.tokens
		series.Tokens = &n
	}
	for _, currency := range currencies {
		if amount, ok := a.cost[currency]; ok {
			series.Costs = append(series.Costs, timelineCost{currency, amount, a.estimated[currency]})
		}
	}
	return series
}
