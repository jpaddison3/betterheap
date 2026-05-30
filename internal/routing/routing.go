// Package routing is betterheap's killer feature: it routes a time window across
// the live hot buffer and the S3 archive so "the window you ask for is the
// window you get." It probes the live buffer horizon (min(dt)), decides which
// tier(s) a [since, until] window touches, and partitions at the horizon so the
// boundary never duplicates or drops rows.
package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jpaddison3/betterheap/internal/client"
	"github.com/jpaddison3/betterheap/internal/source"
)

// TierMode is the routing override requested by --tier/--live.
type TierMode int

const (
	// TierAuto routes by where the window falls relative to the buffer horizon.
	TierAuto TierMode = iota
	// TierLive forces the live hot buffer only.
	TierLive
	// TierArchive forces the S3 archive only.
	TierArchive
	// TierBoth forces both tiers.
	TierBoth
)

// ParseTierMode parses a --tier value.
func ParseTierMode(s string) (TierMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return TierAuto, nil
	case "live":
		return TierLive, nil
	case "archive":
		return TierArchive, nil
	case "both":
		return TierBoth, nil
	default:
		return TierAuto, fmt.Errorf("invalid --tier %q (want auto|live|archive|both)", s)
	}
}

// Window is the requested time range; nil bounds are open.
type Window struct {
	Since *time.Time
	Until *time.Time
}

// TierQuery is one tier's contribution to a plan: a table expression plus the
// dt/_row_type predicates that scope it.
type TierQuery struct {
	Tier  string // "live" | "archive"
	Table string // e.g. remote(t<team>_<source>_logs)
	Where []string
}

// Plan is the resolved routing decision for a query.
type Plan struct {
	Tiers    []TierQuery
	Horizon  *time.Time
	Mode     TierMode
	Warnings []string
}

// TiersHit summarizes which tiers the plan reads, for --explain.
func (p *Plan) TiersHit() string {
	var live, archive bool
	for _, t := range p.Tiers {
		switch t.Tier {
		case "live":
			live = true
		case "archive":
			archive = true
		}
	}
	switch {
	case live && archive:
		return "both"
	case live:
		return "live"
	case archive:
		return "archive"
	default:
		return "none"
	}
}

// Engine builds plans, probing and caching the buffer horizon per source.
type Engine struct {
	qc       *client.QueryClient
	cacheDir string // "" disables the horizon cache
	ttl      time.Duration
}

// NewEngine builds a routing engine. cacheDir "" disables the horizon cache.
func NewEngine(qc *client.QueryClient, cacheDir string) *Engine {
	return &Engine{qc: qc, cacheDir: cacheDir, ttl: 30 * time.Second}
}

// Plan resolves which tier(s) to query and how to scope each, probing the live
// buffer horizon as needed. allTime permits an otherwise-refused unbounded
// archive scan.
func (e *Engine) Plan(ctx context.Context, src *source.Source, win Window, mode TierMode, allTime bool) (*Plan, error) {
	var horizon *time.Time
	var probeWarn string
	if mode == TierAuto || mode == TierBoth {
		h, err := e.horizon(ctx, src)
		if err != nil {
			probeWarn = fmt.Sprintf("could not probe live buffer horizon: %v", err)
		} else {
			horizon = h
		}
	}
	p, err := Decide(src, win, mode, horizon, allTime)
	if err != nil {
		return nil, err
	}
	if probeWarn != "" {
		p.Warnings = append([]string{probeWarn}, p.Warnings...)
	}
	return p, nil
}

// Decide is the pure routing decision: given a window, mode, and (optional)
// horizon, it produces the tier queries. It performs no I/O, so it is the unit
// boundary for testing routing behavior.
func Decide(src *source.Source, win Window, mode TierMode, horizon *time.Time, allTime bool) (*Plan, error) {
	p := &Plan{Mode: mode, Horizon: horizon}

	var useLive, useArchive bool
	var lLo, lHi, aLo, aHi *time.Time

	switch mode {
	case TierLive:
		useLive = true
		lLo, lHi = win.Since, win.Until
	case TierArchive:
		useArchive = true
		aLo, aHi = win.Since, win.Until
	case TierBoth:
		useLive, useArchive = true, true
		if horizon != nil {
			aLo, aHi = win.Since, horizon
			lLo, lHi = horizon, win.Until
		} else {
			lLo, lHi, aLo, aHi = win.Since, win.Until, win.Since, win.Until
			p.Warnings = append(p.Warnings, "buffer horizon unknown; boundary rows may appear in both tiers")
		}
	case TierAuto:
		switch {
		case win.Since == nil && win.Until == nil:
			useLive = true // recent default: fast path
		case horizon == nil:
			useLive, useArchive = true, true
			lLo, lHi, aLo, aHi = win.Since, win.Until, win.Since, win.Until
			p.Warnings = append(p.Warnings, "buffer horizon unknown; querying both tiers (boundary rows may duplicate)")
		default:
			liveNeeded := win.Until == nil || !win.Until.Before(*horizon)
			archiveNeeded := win.Since == nil || win.Since.Before(*horizon)
			useLive, useArchive = liveNeeded, archiveNeeded
			switch {
			case useLive && useArchive:
				aLo, aHi = win.Since, horizon
				lLo, lHi = horizon, win.Until
			case useLive:
				lLo, lHi = win.Since, win.Until
			default:
				aLo, aHi = win.Since, win.Until
			}
		}
	}

	if useArchive && !allTime && win.Since == nil && win.Until == nil {
		return nil, fmt.Errorf("refusing to scan the S3 archive without a time bound: pass --since (e.g. --since 7d), --until, or --all-time to override")
	}

	if useLive {
		p.Tiers = append(p.Tiers, TierQuery{
			Tier:  "live",
			Table: fmt.Sprintf("remote(%s)", src.LiveTable()),
			Where: dtPreds(lLo, lHi, false),
		})
	}
	if useArchive {
		exclusiveHi := useLive && horizon != nil && aHi != nil && aHi.Equal(*horizon)
		w := append([]string{"_row_type = 1"}, dtPreds(aLo, aHi, exclusiveHi)...)
		p.Tiers = append(p.Tiers, TierQuery{
			Tier:  "archive",
			Table: fmt.Sprintf("s3Cluster(primary, %s)", src.ArchiveTable()),
			Where: w,
		})
	}
	return p, nil
}

// dtPreds builds the dt range predicates. exclusiveHi makes the upper bound `<`
// instead of `<=`, used to partition the archive just below the live horizon.
func dtPreds(lo, hi *time.Time, exclusiveHi bool) []string {
	var out []string
	if lo != nil {
		out = append(out, fmt.Sprintf("dt >= '%s'", FormatDT(*lo)))
	}
	if hi != nil {
		op := "<="
		if exclusiveHi {
			op = "<"
		}
		out = append(out, fmt.Sprintf("dt %s '%s'", op, FormatDT(*hi)))
	}
	return out
}

// horizon returns the live buffer's min(dt), cached briefly. A nil result means
// the buffer is empty or the horizon is unknown.
func (e *Engine) horizon(ctx context.Context, src *source.Source) (*time.Time, error) {
	key := fmt.Sprintf("horizon-%d-%s.json", src.TeamID, src.TableName)
	if t, ok := e.readHorizonCache(key); ok {
		return t, nil
	}
	sql := fmt.Sprintf("SELECT min(dt) AS h FROM remote(%s)", src.LiveTable())
	s, err := e.qc.QueryScalar(ctx, sql)
	if err != nil {
		return nil, err
	}
	t := parseHorizon(s)
	e.writeHorizonCache(key, t)
	return t, nil
}

// parseHorizon parses a min(dt) probe result. ClickHouse returns the epoch for
// an empty table, which we treat as "unknown" (nil).
func parseHorizon(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			t = t.UTC()
			if t.Year() <= 1971 {
				return nil
			}
			return &t
		}
	}
	return nil
}

type horizonCache struct {
	ProbedAt time.Time  `json:"probed_at"`
	DT       *time.Time `json:"dt"`
}

func (e *Engine) readHorizonCache(key string) (*time.Time, bool) {
	if e.cacheDir == "" {
		return nil, false
	}
	b, err := os.ReadFile(filepath.Join(e.cacheDir, key))
	if err != nil {
		return nil, false
	}
	var hc horizonCache
	if err := json.Unmarshal(b, &hc); err != nil {
		return nil, false
	}
	if time.Since(hc.ProbedAt) > e.ttl {
		return nil, false
	}
	return hc.DT, true
}

func (e *Engine) writeHorizonCache(key string, dt *time.Time) {
	if e.cacheDir == "" {
		return
	}
	b, err := json.Marshal(horizonCache{ProbedAt: time.Now(), DT: dt})
	if err != nil {
		return
	}
	if err := os.MkdirAll(e.cacheDir, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(e.cacheDir, key), b, 0o600)
}
