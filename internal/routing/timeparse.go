package routing

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// relDur matches a single relative duration like "7d", "24h", "30m", "2w".
var relDur = regexp.MustCompile(`^(\d+)(s|m|h|d|w)$`)

// timeLayouts are the absolute formats accepted for --since/--until, all
// interpreted as UTC (Better Stack stores `dt` in UTC).
var timeLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04",
	"2006-01-02",
}

// ParseTime resolves a --since/--until value relative to now (which should be
// UTC). It accepts:
//   - keywords: now, today, yesterday
//   - relative durations interpreted as "ago": 7d, 24h, 30m, 90s, 2w, 1h30m
//   - absolute UTC timestamps: 2026-05-28, 2026-05-28 04:19:53, RFC3339
func ParseTime(now time.Time, s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time value")
	}
	now = now.UTC()

	switch strings.ToLower(s) {
	case "now":
		return now, nil
	case "today":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC), nil
	case "yesterday":
		t := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		return t.AddDate(0, 0, -1), nil
	}

	if m := relDur.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return time.Time{}, err
		}
		var d time.Duration
		switch m[2] {
		case "s":
			d = time.Duration(n) * time.Second
		case "m":
			d = time.Duration(n) * time.Minute
		case "h":
			d = time.Duration(n) * time.Hour
		case "d":
			d = time.Duration(n) * 24 * time.Hour
		case "w":
			d = time.Duration(n) * 7 * 24 * time.Hour
		}
		return now.Add(-d), nil
	}

	// Composite Go durations like "1h30m" — also interpreted as "ago".
	if d, err := time.ParseDuration(s); err == nil {
		return now.Add(-d), nil
	}

	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized time %q (use 7d, 24h, 2026-05-28, or 2026-05-28 04:19:53)", s)
}

// FormatDT renders a time as a ClickHouse-comparable UTC literal value (no
// quotes), e.g. "2026-05-28 04:19:53".
func FormatDT(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05")
}
