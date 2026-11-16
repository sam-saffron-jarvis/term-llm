package proxy

import (
	"fmt"
	"time"
)

func timelineBounds(now time.Time, loc *time.Location, period string) (time.Time, []time.Time, bool, error) {
	local := now.In(loc)
	hourly := period == "today"
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	switch period {
	case "today":
	case "7d":
		start = start.AddDate(0, 0, -6)
	case "30d":
		start = start.AddDate(0, 0, -29)
	default:
		return time.Time{}, nil, false, fmt.Errorf("invalid range")
	}
	bounds := []time.Time{start}
	if hourly {
		for t := start.Add(time.Hour); t.Before(now); t = t.Add(time.Hour) {
			bounds = append(bounds, t)
		}
	} else {
		for t := start.AddDate(0, 0, 1); t.Before(now); t = t.AddDate(0, 0, 1) {
			bounds = append(bounds, t)
		}
	}
	bounds = append(bounds, now)
	return start, bounds, hourly, nil
}
