package routing

import (
	"reflect"
	"testing"
	"time"

	"github.com/jpaddison3/betterheap/internal/source"
)

func tp(t time.Time) *time.Time { return &t }

var (
	testSrc = &source.Source{TeamID: 99999, TableName: "myapp"}
	horizon = time.Date(2026, 5, 28, 4, 20, 33, 0, time.UTC)
)

func plan(t *testing.T, win Window, mode TierMode, h *time.Time, allTime bool) *Plan {
	t.Helper()
	p, err := Decide(testSrc, win, mode, h, allTime)
	if err != nil {
		t.Fatalf("Decide error: %v", err)
	}
	return p
}

func tier(p *Plan, name string) *TierQuery {
	for i := range p.Tiers {
		if p.Tiers[i].Tier == name {
			return &p.Tiers[i]
		}
	}
	return nil
}

func TestAutoNoBoundsIsLiveOnly(t *testing.T) {
	p := plan(t, Window{}, TierAuto, &horizon, false)
	if p.TiersHit() != "live" {
		t.Fatalf("tiers = %s; want live", p.TiersHit())
	}
	if lv := tier(p, "live"); len(lv.Where) != 0 {
		t.Errorf("live where = %v; want none", lv.Where)
	}
}

func TestAutoStraddlePartitionsAtHorizon(t *testing.T) {
	since := time.Date(2026, 5, 28, 2, 0, 0, 0, time.UTC)
	p := plan(t, Window{Since: tp(since)}, TierAuto, &horizon, false)
	if p.TiersHit() != "both" {
		t.Fatalf("tiers = %s; want both", p.TiersHit())
	}
	live := tier(p, "live")
	if want := []string{"dt >= '2026-05-28 04:20:33'"}; !reflect.DeepEqual(live.Where, want) {
		t.Errorf("live where = %v; want %v", live.Where, want)
	}
	arch := tier(p, "archive")
	want := []string{"_row_type = 1", "dt >= '2026-05-28 02:00:00'", "dt < '2026-05-28 04:20:33'"}
	if !reflect.DeepEqual(arch.Where, want) {
		t.Errorf("archive where = %v; want %v", arch.Where, want)
	}
}

func TestAutoEntirelyArchive(t *testing.T) {
	since := time.Date(2026, 5, 28, 2, 0, 0, 0, time.UTC)
	until := time.Date(2026, 5, 28, 3, 0, 0, 0, time.UTC)
	p := plan(t, Window{Since: tp(since), Until: tp(until)}, TierAuto, &horizon, false)
	if p.TiersHit() != "archive" {
		t.Fatalf("tiers = %s; want archive", p.TiersHit())
	}
	arch := tier(p, "archive")
	// Upper bound is inclusive (<=) because we are not partitioning against live.
	want := []string{"_row_type = 1", "dt >= '2026-05-28 02:00:00'", "dt <= '2026-05-28 03:00:00'"}
	if !reflect.DeepEqual(arch.Where, want) {
		t.Errorf("archive where = %v; want %v", arch.Where, want)
	}
}

func TestAutoEntirelyLive(t *testing.T) {
	since := time.Date(2026, 5, 28, 5, 0, 0, 0, time.UTC) // newer than horizon
	p := plan(t, Window{Since: tp(since)}, TierAuto, &horizon, false)
	if p.TiersHit() != "live" {
		t.Fatalf("tiers = %s; want live", p.TiersHit())
	}
}

func TestArchiveGuardBlocksUnboundedScan(t *testing.T) {
	_, err := Decide(testSrc, Window{}, TierArchive, nil, false)
	if err == nil {
		t.Fatal("expected archive guard error for unbounded scan")
	}
}

func TestArchiveAllTimeOverride(t *testing.T) {
	p := plan(t, Window{}, TierArchive, nil, true)
	arch := tier(p, "archive")
	if arch == nil || !reflect.DeepEqual(arch.Where, []string{"_row_type = 1"}) {
		t.Fatalf("archive where = %v; want [_row_type = 1]", arch)
	}
	if arch.Table != "s3Cluster(primary, t99999_myapp_s3)" {
		t.Errorf("archive table = %q", arch.Table)
	}
}

func TestForceLive(t *testing.T) {
	since := time.Date(2026, 5, 28, 5, 0, 0, 0, time.UTC)
	p := plan(t, Window{Since: tp(since)}, TierLive, nil, false)
	if p.TiersHit() != "live" {
		t.Fatalf("tiers = %s; want live", p.TiersHit())
	}
	live := tier(p, "live")
	if live.Table != "remote(t99999_myapp_logs)" {
		t.Errorf("live table = %q", live.Table)
	}
}

func TestBothWithoutHorizonWarns(t *testing.T) {
	since := time.Date(2026, 5, 28, 2, 0, 0, 0, time.UTC)
	p := plan(t, Window{Since: tp(since)}, TierBoth, nil, false)
	if p.TiersHit() != "both" {
		t.Fatalf("tiers = %s; want both", p.TiersHit())
	}
	if len(p.Warnings) == 0 {
		t.Error("expected a warning when horizon is unknown")
	}
}

func TestParseTierMode(t *testing.T) {
	cases := map[string]TierMode{"": TierAuto, "auto": TierAuto, "live": TierLive, "archive": TierArchive, "both": TierBoth}
	for in, want := range cases {
		got, err := ParseTierMode(in)
		if err != nil || got != want {
			t.Errorf("ParseTierMode(%q) = %v,%v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseTierMode("bogus"); err == nil {
		t.Error("expected error for bogus tier")
	}
}
