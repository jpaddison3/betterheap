package cli

import (
	"github.com/spf13/cobra"

	"github.com/jpaddison3/betterheap/internal/query"
)

var statsBy string

var statsCmd = &cobra.Command{
	Use:   "stats [source]",
	Short: "Aggregate log counts by a dimension (tier-aware)",
	Long: `Counts rows grouped by --by (level, module, day, hour, status, path, env,
request_id) over the window, summed across the live and archive tiers. Combine
with --level/--module/--where to scope the population.`,
	Example: `  betterheap stats --by level --since 7d
  betterheap stats --by module --level error --since 30d
  betterheap stats --by day --level error --since 14d`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStats(sourceArg(args))
	},
}

func runStats(sourceName string) error {
	a, err := newAppEnv()
	if err != nil {
		return err
	}
	src, prof, plan, err := a.routeFor(sourceName, "24h")
	if err != nil {
		return err
	}
	_ = src

	filter := query.Filter{Level: qf.level, Module: qf.module, Where: qf.where}
	sql, col, err := query.BuildStatsSQL(plan, prof, filter, statsBy, a.cfg.Limit(qf.limit))
	if err != nil {
		return err
	}

	fields := []string{col, "count"}
	write, closeFn, err := a.outputSink(fields)
	if err != nil {
		return err
	}
	n, err := a.qc.Stream(ctx(), sql, write)
	if err != nil {
		return classify(err)
	}
	if cerr := closeFn(); cerr != nil {
		return cerr
	}
	if qf.explain {
		explainPlan(sql, plan, n)
	}
	if n == 0 {
		return exitErr{code: 3}
	}
	return nil
}

func init() {
	addRangeFlags(statsCmd)
	statsCmd.Flags().StringVar(&statsBy, "by", "level", "group by: level|module|day|hour|status|path|env|request_id")
	statsCmd.Flags().StringVar(&qf.level, "level", "", "restrict to an exact level")
	statsCmd.Flags().StringVar(&qf.module, "module", "", "restrict to an exact module")
}
