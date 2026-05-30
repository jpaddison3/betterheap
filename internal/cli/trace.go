package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jpaddison3/betterheap/internal/client"
	"github.com/jpaddison3/betterheap/internal/query"
	"github.com/jpaddison3/betterheap/internal/routing"
)

var traceCmd = &cobra.Command{
	Use:   "trace <request_id> [source...]",
	Short: "All logs for a request id, across both tiers and sources, merged by time",
	Long: `Collects every log line for a request id across the live and archive tiers,
and across one or more sources, merged into chronological order. With no --since
it scans the full archive for the id (selective but slow); pass --since to bound
it. Multiple sources can be listed; per-source failures are reported, not fatal.`,
	Example: `  betterheap trace qfjk8-1780116472423-31bd84ea8a85
  betterheap trace <id> web api --since 1d`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runTrace(args[0], args[1:])
	},
}

func runTrace(reqID string, sources []string) error {
	if strings.TrimSpace(reqID) == "" {
		return fmt.Errorf("empty request id")
	}
	a, err := newAppEnv()
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		sources = []string{a.cfg.Source("")}
	}

	allTime := qf.allTime
	if qf.since == "" && qf.until == "" && !allTime {
		allTime = true
		warn(fmt.Sprintf("no --since given; scanning the full archive for request %s (pass --since to speed this up)", reqID))
	}

	fields := []string{"dt", "source", "level", "module", "message"}
	if qf.fields != "" {
		fields = splitFields(qf.fields)
	}
	sqlFields := removeString(fields, "source")

	mode := routing.TierBoth
	if qf.live {
		mode = routing.TierLive
	} else if qf.tier != "" {
		if mode, err = routing.ParseTierMode(qf.tier); err != nil {
			return err
		}
	}

	var rows []client.Row
	var failed []string
	for _, name := range sources {
		src, prof, err := a.resolveSource(name) // also binds the query client to src's region
		if err != nil {
			warn(fmt.Sprintf("%s: %v", name, err))
			failed = append(failed, name)
			continue
		}
		win, err := parseWindow(a.cfg.Since(qf.since), qf.until)
		if err != nil {
			return err
		}
		plan, err := a.eng.Plan(ctx(), src, win, mode, allTime)
		if err != nil {
			warn(fmt.Sprintf("%s: %v", name, err))
			failed = append(failed, name)
			continue
		}
		spec := query.Spec{
			Source:  src,
			Profile: prof,
			Plan:    plan,
			Fields:  sqlFields,
			Filter:  query.Filter{RequestID: reqID, Where: qf.where},
			Limit:   a.cfg.Limit(qf.limit),
			Desc:    false,
		}
		srcName := src.Name
		if _, _, err := query.Run(ctx(), a.qc, spec, func(r client.Row) error {
			r["source"] = srcName
			rows = append(rows, r)
			return nil
		}); err != nil {
			warn(fmt.Sprintf("%s: %v", name, err))
			failed = append(failed, name)
		}
	}

	sort.SliceStable(rows, func(i, j int) bool { return dtString(rows[i]) < dtString(rows[j]) })

	write, closeFn, err := a.outputSink(fields)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := write(r); err != nil {
			return err
		}
	}
	if cerr := closeFn(); cerr != nil {
		return cerr
	}

	switch {
	case len(rows) == 0 && len(failed) > 0:
		return exitErr{code: 1, err: fmt.Errorf("all %d source(s) failed", len(failed))}
	case len(failed) > 0:
		warn(fmt.Sprintf("%d of %d sources failed: %v", len(failed), len(sources), failed))
		return exitErr{code: 4}
	case len(rows) == 0:
		return exitErr{code: 3}
	}
	return nil
}

func removeString(ss []string, drop string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s != drop {
			out = append(out, s)
		}
	}
	return out
}

func init() {
	addRangeFlags(traceCmd)
}
