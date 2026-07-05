package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"

	"github.com/jpaddison3/betterheap/internal/client"
	"github.com/jpaddison3/betterheap/internal/output"
	"github.com/jpaddison3/betterheap/internal/query"
	"github.com/jpaddison3/betterheap/internal/routing"
)

const followLimit = 1000

var tailCmd = &cobra.Command{
	Use:   "tail [source]",
	Short: "Recent logs from the live buffer; -f to follow",
	Long: `Shows the most recent logs in chronological order (newest last). By default
it reads the live hot buffer only (fast). Pass --since to widen across tiers, or
-f/--follow to stream new rows as they arrive.`,
	Example: `  betterheap tail
  betterheap tail -f
  betterheap tail --since 1h --level error
  betterheap tail -n 50 --module jobRecommender`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runTail(sourceArg(args))
	},
}

func init() {
	addRangeFlags(tailCmd)
	tailCmd.Flags().BoolVarP(&qf.follow, "follow", "f", false, "stream new rows as they arrive (live buffer)")
	tailCmd.Flags().DurationVar(&qf.interval, "interval", 2*time.Second, "poll interval for --follow")
	tailCmd.Flags().StringVar(&qf.level, "level", "", "exact level match, e.g. error")
	tailCmd.Flags().StringVar(&qf.module, "module", "", "exact module match")
}

func runTail(srcName string) error {
	c, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	a, err := newAppEnv()
	if err != nil {
		return err
	}
	src, prof, err := a.resolveSource(srcName)
	if err != nil {
		return classify(err)
	}

	fields := defaultLogFields
	if qf.fields != "" {
		fields = splitFields(qf.fields)
	}
	filter := query.Filter{Level: qf.level, Module: qf.module, Where: qf.where}
	limit := a.cfg.Limit(qf.limit)

	isTTY := output.IsTTY(os.Stdout)
	format, err := output.ResolveFormat(a.cfg.Format(flagFormat), isTTY)
	if err != nil {
		return err
	}
	color := output.ColorEnabled(flagNoColor, isTTY)

	// Routing: tail reads the live buffer by default; --since/--tier widen it,
	// but -f always follows live.
	mode := routing.TierLive
	switch {
	case qf.live:
		mode = routing.TierLive
	case qf.tier != "":
		if mode, err = routing.ParseTierMode(qf.tier); err != nil {
			return err
		}
	case qf.since != "" || qf.until != "":
		mode = routing.TierAuto
	}
	if qf.follow && mode != routing.TierLive {
		warn("-f follows the live buffer; ignoring tier/since override")
		mode = routing.TierLive
	}

	win, err := parseWindow(a.cfg.Since(qf.since), qf.until)
	if err != nil {
		return err
	}
	if qf.follow {
		win = routing.Window{} // live buffer, unbounded recent
	}
	plan, err := a.eng.Plan(ctx(), src, win, mode, qf.allTime)
	if err != nil {
		return err
	}
	for _, w := range plan.Warnings {
		warn(w)
	}

	// Initial batch: newest N, then reversed to read chronologically.
	spec := query.Spec{Source: src, Profile: prof, Plan: plan, Fields: fields, Filter: filter, Limit: limit, Desc: true}
	var rows []client.Row
	if _, _, err := query.Run(ctx(), a.qc, spec, func(r client.Row) error {
		rows = append(rows, r)
		return nil
	}); err != nil {
		return classify(err)
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}

	if !qf.follow {
		sink := output.NewSink(os.Stdout, format, fields, color, qf.full)
		for _, r := range rows {
			if err := sink.Write(r); err != nil {
				return err
			}
		}
		if err := sink.Close(); err != nil {
			return err
		}
		if len(rows) == 0 {
			return exitErr{code: 3}
		}
		return nil
	}

	// Follow: print the initial batch, then poll the live buffer for dt > maxDt.
	writeRow := tailWriter(format, fields, color, isTTY, qf.full)
	maxDt := ""
	for _, r := range rows {
		if err := writeRow(r); err != nil {
			return err
		}
		maxDt = laterDT(maxDt, dtString(r))
	}
	if maxDt == "" {
		maxDt = routing.FormatDT(time.Now().UTC())
	}

	liveTable := fmt.Sprintf("remote(%s)", src.LiveTable())
	for {
		select {
		case <-c.Done():
			return nil
		default:
		}
		fp := &routing.Plan{Tiers: []routing.TierQuery{{
			Tier:  "live",
			Table: liveTable,
			Where: []string{fmt.Sprintf("dt > '%s'", maxDt)},
		}}}
		fspec := query.Spec{Source: src, Profile: prof, Plan: fp, Fields: fields, Filter: filter, Limit: followLimit, Desc: false}
		_, _, err := query.Run(c, a.qc, fspec, func(r client.Row) error {
			maxDt = laterDT(maxDt, dtString(r))
			return writeRow(r)
		})
		if err != nil {
			if c.Err() != nil {
				return nil
			}
			warn(err.Error())
		}
		select {
		case <-c.Done():
			return nil
		case <-time.After(qf.interval):
		}
	}
}

// tailWriter returns a per-row writer suited to following: ndjson when piped or
// requested, otherwise a compact (optionally colored) line.
func tailWriter(format output.Format, fields []string, color, isTTY, full bool) func(client.Row) error {
	if format == output.FormatNDJSON || !isTTY {
		s := output.NewSink(os.Stdout, output.FormatNDJSON, fields, false, full)
		return s.Write
	}
	if format == output.FormatCSV || format == output.FormatJSON || format == output.FormatTable {
		warn(fmt.Sprintf("--format %s can't stream with -f; using line output", format))
	}
	return func(r client.Row) error {
		_, err := fmt.Fprintln(os.Stdout, output.Line(fields, r, color, full))
		return err
	}
}

func dtString(r client.Row) string {
	if v, ok := r["dt"]; ok {
		return fmt.Sprint(v)
	}
	return ""
}

// laterDT returns the lexicographically greater timestamp (ISO strings sort
// chronologically).
func laterDT(a, b string) string {
	if b > a {
		return b
	}
	return a
}
