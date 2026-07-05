package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jpaddison3/betterheap/internal/client"
	"github.com/jpaddison3/betterheap/internal/envelope"
	"github.com/jpaddison3/betterheap/internal/output"
	"github.com/jpaddison3/betterheap/internal/query"
	"github.com/jpaddison3/betterheap/internal/routing"
	"github.com/jpaddison3/betterheap/internal/source"
)

// Exit-code scheme: 0 ok, 1 query error, 2 auth error,
// 3 no results, 4 partial-tier (reserved).
type exitErr struct {
	code int
	err  error
}

func (e exitErr) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit status %d", e.code)
	}
	return e.err.Error()
}

func asExit(err error, target *exitErr) bool { return errors.As(err, target) }

// classify maps a runtime error to an exit code (2 for auth failures, else 1).
func classify(err error) error {
	if err == nil {
		return nil
	}
	var qe *client.QueryError
	if errors.As(err, &qe) && (qe.StatusCode == 401 || qe.StatusCode == 403) {
		return exitErr{code: 2, err: err}
	}
	var ae *client.APIError
	if errors.As(err, &ae) && (ae.StatusCode == 401 || ae.StatusCode == 403) {
		return exitErr{code: 2, err: err}
	}
	return exitErr{code: 1, err: err}
}

// qf holds flag values shared by the query commands. Only one command runs per
// process, so a single shared struct is safe.
var qf struct {
	since, until, tier     string
	live, allTime, explain bool
	full                   bool
	fields                 string
	jq                     string
	limit                  int
	where                  []string
	level, module, search  string
	follow                 bool
	interval               time.Duration
}

// addRangeFlags registers the time-routing and output flags common to logs,
// errors, warnings, and search.
func addRangeFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&qf.since, "since", "", "window start: 7d, 24h, 2026-05-28, '2026-05-28 04:19:53'")
	f.StringVar(&qf.until, "until", "", "window end (default: now)")
	f.StringVar(&qf.tier, "tier", "", "force tier: auto|live|archive|both (default: auto)")
	f.BoolVar(&qf.live, "live", false, "shortcut for --tier live (fast, buffer-only)")
	f.BoolVar(&qf.allTime, "all-time", false, "permit an archive query with no time bound")
	f.IntVarP(&qf.limit, "limit", "n", 0, "max rows (default: 100 or config)")
	f.StringVar(&qf.fields, "fields", "", "comma-separated output fields, in order")
	f.StringArrayVar(&qf.where, "where", nil, "predicate key<op>value, op in = != > < >= <= (repeatable)")
	f.StringVar(&qf.jq, "jq", "", "transform each row with an embedded jq program")
	f.BoolVar(&qf.full, "full", false, fmt.Sprintf("emit full field values (default: truncate strings over %d chars)", output.TruncateLimit))
	f.BoolVar(&qf.explain, "explain", false, "print generated SQL, tiers hit, and row count to stderr")
}

func splitFields(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func sourceArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

func parseWindow(sinceVal, untilVal string) (routing.Window, error) {
	var w routing.Window
	now := time.Now().UTC()
	if sinceVal != "" {
		t, err := routing.ParseTime(now, sinceVal)
		if err != nil {
			return w, fmt.Errorf("--since: %w", err)
		}
		w.Since = &t
	}
	if untilVal != "" {
		t, err := routing.ParseTime(now, untilVal)
		if err != nil {
			return w, fmt.Errorf("--until: %w", err)
		}
		w.Until = &t
	}
	if w.Since != nil && w.Until != nil && w.Until.Before(*w.Since) {
		return w, fmt.Errorf("--until (%s) is before --since (%s)", routing.FormatDT(*w.Until), routing.FormatDT(*w.Since))
	}
	return w, nil
}

func tierMode() (routing.TierMode, error) {
	if qf.live {
		return routing.TierLive, nil
	}
	return routing.ParseTierMode(qf.tier)
}

func (a *appEnv) resolveSource(name string) (*source.Source, envelope.Profile, error) {
	name = a.cfg.Source(name)
	src, err := a.reg.Resolve(ctx(), name)
	if err != nil {
		return nil, envelope.Profile{}, err
	}
	a.bindRegion(src.Region)
	pname := a.cfg.Profile(flagProfile)
	if pname == "" {
		pname = src.Profile
	}
	prof, ok := envelope.Get(pname)
	if !ok {
		return nil, envelope.Profile{}, fmt.Errorf("unknown profile %q (want %s)", pname, strings.Join(envelope.Names(), "|"))
	}
	return src, prof, nil
}

// bindRegion repoints the query client and routing engine at a source's own
// region, since each Better Stack source is queryable only via its regional
// connect endpoint. A user-supplied --region (or full --query-host) wins.
func (a *appEnv) bindRegion(region string) {
	if flagRegion != "" || region == "" {
		return
	}
	a.qc = client.NewQueryClient(a.cfg.QueryHost(""), region,
		a.cfg.QueryUsername(flagQueryUser), a.cfg.QueryPassword(flagQueryPass), flagVerbose)
	a.eng = routing.NewEngine(a.qc, a.cacheDir)
}

// runOpts configures a friendly query command.
type runOpts struct {
	source       string
	fields       []string     // default output fields if --fields is unset
	filter       query.Filter // command-specific base filter
	desc         bool         // newest-first ordering
	defaultSince string       // window start when neither flag nor config set one
}

// routeFor resolves the source/profile, applies the window (with an optional
// command default), and produces the routing plan — the setup shared by the
// query and stats commands. Warnings are emitted to stderr here.
func (a *appEnv) routeFor(sourceName, defaultSince string) (*source.Source, envelope.Profile, *routing.Plan, error) {
	src, prof, err := a.resolveSource(sourceName)
	if err != nil {
		return nil, envelope.Profile{}, nil, classify(err)
	}
	sinceVal := a.cfg.Since(qf.since)
	if sinceVal == "" && defaultSince != "" {
		sinceVal = defaultSince
		warn(fmt.Sprintf("no --since given; defaulting to %s across both tiers (widen with --since or --all-time)", defaultSince))
	}
	win, err := parseWindow(sinceVal, qf.until)
	if err != nil {
		return nil, envelope.Profile{}, nil, err
	}
	mode, err := tierMode()
	if err != nil {
		return nil, envelope.Profile{}, nil, err
	}
	plan, err := a.eng.Plan(ctx(), src, win, mode, qf.allTime)
	if err != nil {
		return nil, envelope.Profile{}, nil, err
	}
	for _, w := range plan.Warnings {
		warn(w)
	}
	return src, prof, plan, nil
}

// runQuery is the shared path for logs/errors/warnings/search: resolve source,
// route the window, build + run the SQL, and stream rows to the chosen format.
func runQuery(o runOpts) error {
	a, err := newAppEnv()
	if err != nil {
		return err
	}
	src, prof, plan, err := a.routeFor(o.source, o.defaultSince)
	if err != nil {
		return err
	}

	fields := o.fields
	if qf.fields != "" {
		fields = splitFields(qf.fields)
	}
	o.filter.Where = append(o.filter.Where, qf.where...)

	spec := query.Spec{
		Source:  src,
		Profile: prof,
		Plan:    plan,
		Fields:  fields,
		Filter:  o.filter,
		Limit:   a.cfg.Limit(qf.limit),
		Desc:    o.desc,
	}

	write, closeFn, err := a.outputSink(fields)
	if err != nil {
		return err
	}
	sql, n, err := query.Run(ctx(), a.qc, spec, write)
	if err != nil {
		return classify(err)
	}
	if cerr := closeFn(); cerr != nil {
		return cerr
	}
	if qf.explain {
		explainPlan(sql, plan, n)
	}
	if spec.Limit > 0 && n == spec.Limit {
		warn(fmt.Sprintf("hit --limit %d; more rows likely exist — raise -n or narrow --since/--until", spec.Limit))
	}
	if n == 0 {
		return exitErr{code: 3}
	}
	return nil
}

// outputSink returns a row writer + closer for the resolved format, switching to
// the embedded-jq sink when --jq is set.
func (a *appEnv) outputSink(fields []string) (func(client.Row) error, func() error, error) {
	isTTY := output.IsTTY(os.Stdout)
	format, err := output.ResolveFormat(a.cfg.Format(flagFormat), isTTY)
	if err != nil {
		return nil, nil, err
	}
	if qf.jq != "" {
		js, err := output.NewJQSink(os.Stdout, qf.jq, format)
		if err != nil {
			return nil, nil, err
		}
		return js.Write, js.Close, nil
	}
	sink := output.NewSink(os.Stdout, format, fields, output.ColorEnabled(flagNoColor, isTTY), qf.full)
	return sink.Write, sink.Close, nil
}

func warn(msg string) {
	fmt.Fprintln(os.Stderr, "warning:", msg)
}

func explainPlan(sql string, plan *routing.Plan, rows int) {
	horizon := "unknown"
	if plan.Horizon != nil {
		horizon = routing.FormatDT(*plan.Horizon)
	}
	fmt.Fprintf(os.Stderr, "-- tiers: %s | horizon: %s | rows: %d\n", plan.TiersHit(), horizon, rows)
	fmt.Fprintln(os.Stderr, sql)
}
