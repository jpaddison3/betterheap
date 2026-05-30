package routing

import (
	"testing"
	"time"
)

var refNow = time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

func TestParseTimeRelative(t *testing.T) {
	cases := map[string]time.Time{
		"7d":  refNow.Add(-7 * 24 * time.Hour),
		"24h": refNow.Add(-24 * time.Hour),
		"30m": refNow.Add(-30 * time.Minute),
		"2w":  refNow.Add(-14 * 24 * time.Hour),
		"90s": refNow.Add(-90 * time.Second),
	}
	for in, want := range cases {
		got, err := ParseTime(refNow, in)
		if err != nil {
			t.Fatalf("ParseTime(%q) error: %v", in, err)
		}
		if !got.Equal(want) {
			t.Errorf("ParseTime(%q) = %v; want %v", in, got, want)
		}
	}
}

func TestParseTimeCompositeDuration(t *testing.T) {
	got, err := ParseTime(refNow, "1h30m")
	if err != nil {
		t.Fatal(err)
	}
	if want := refNow.Add(-90 * time.Minute); !got.Equal(want) {
		t.Errorf("1h30m = %v; want %v", got, want)
	}
}

func TestParseTimeAbsolute(t *testing.T) {
	got, err := ParseTime(refNow, "2026-05-28")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("date = %v; want %v", got, want)
	}

	got, err = ParseTime(refNow, "2026-05-28 04:19:53")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 5, 28, 4, 19, 53, 0, time.UTC); !got.Equal(want) {
		t.Errorf("datetime = %v; want %v", got, want)
	}
}

func TestParseTimeKeywords(t *testing.T) {
	got, err := ParseTime(refNow, "now")
	if err != nil || !got.Equal(refNow) {
		t.Errorf("now = %v,%v; want %v", got, err, refNow)
	}
	got, _ = ParseTime(refNow, "today")
	if want := time.Date(2026, 5, 30, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("today = %v; want %v", got, want)
	}
}

func TestParseTimeErrors(t *testing.T) {
	for _, in := range []string{"", "tomorrowish", "2026/05/28", "5 days"} {
		if _, err := ParseTime(refNow, in); err == nil {
			t.Errorf("ParseTime(%q) expected error", in)
		}
	}
}

func TestFormatDT(t *testing.T) {
	got := FormatDT(time.Date(2026, 5, 28, 4, 19, 53, 0, time.UTC))
	if got != "2026-05-28 04:19:53" {
		t.Errorf("FormatDT = %q", got)
	}
}
