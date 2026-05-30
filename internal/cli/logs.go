package cli

import (
	"github.com/spf13/cobra"

	"github.com/jpaddison3/betterheap/internal/query"
)

var (
	defaultLogFields   = []string{"dt", "level", "module", "message"}
	defaultLevelFields = []string{"dt", "module", "message"}
)

var logsCmd = &cobra.Command{
	Use:   "logs [source]",
	Short: "Query logs over an arbitrary time window (tier-aware)",
	Long: `The workhorse range query. --since/--until route automatically across the
live buffer and the S3 archive, so the window you ask for is the window you get.

Fields are envelope-aware: --level is an exact match (not a substring), and
--module / --where work against the JSON envelope.`,
	Example: `  betterheap logs --since 7d --level error
  betterheap logs --since 2026-05-28 --until 2026-05-29 --module jobRecommender
  betterheap logs --where status=500 --where env=production --since 24h
  betterheap logs --fields dt,level,path,status --since 1h`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runQuery(runOpts{
			source: sourceArg(args),
			fields: defaultLogFields,
			filter: query.Filter{Level: qf.level, Module: qf.module, Search: qf.search},
			desc:   true,
		})
	},
}

var errorsCmd = &cobra.Command{
	Use:   "errors [source]",
	Short: "Show true error-level logs (exact level='error', not substring)",
	Long: `Fixes bslog's over-match: only rows whose envelope level is exactly "error"
are returned, so an info line merely containing the word "error" is excluded.`,
	Example: `  betterheap errors --since 7d
  betterheap errors --since 30d --module jobRecommender`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runQuery(runOpts{
			source: sourceArg(args),
			fields: defaultLevelFields,
			filter: query.Filter{Level: "error", Module: qf.module},
			desc:   true,
		})
	},
}

var warningsCmd = &cobra.Command{
	Use:     "warnings [source]",
	Short:   "Show true warning-level logs",
	Example: `  betterheap warnings --since 24h`,
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runQuery(runOpts{
			source: sourceArg(args),
			fields: defaultLevelFields,
			filter: query.Filter{Level: "warning", Module: qf.module},
			desc:   true,
		})
	},
}

var searchCmd = &cobra.Command{
	Use:   "search <pattern> [source]",
	Short: "Full-text search over message, across both tiers",
	Long: `Case-insensitive substring search over the message field. Unlike bslog
(which only ever saw ~35 minutes), this spans the archive: with no --since it
searches the last 7 days across both tiers and tells you so on stderr.`,
	Example: `  betterheap search "Failed to truncate" --since 14d
  betterheap search "timeout" --level error --since 30d`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		src := ""
		if len(args) > 1 {
			src = args[1]
		}
		return runQuery(runOpts{
			source:       src,
			fields:       defaultLogFields,
			filter:       query.Filter{Search: args[0], Level: qf.level, Module: qf.module},
			desc:         true,
			defaultSince: "7d",
		})
	},
}

func init() {
	addRangeFlags(logsCmd)
	logsCmd.Flags().StringVar(&qf.level, "level", "", "exact level match, e.g. error|warning|info")
	logsCmd.Flags().StringVar(&qf.module, "module", "", "exact module match")
	logsCmd.Flags().StringVar(&qf.search, "search", "", "case-insensitive substring over message")

	addRangeFlags(errorsCmd)
	errorsCmd.Flags().StringVar(&qf.module, "module", "", "exact module match")

	addRangeFlags(warningsCmd)
	warningsCmd.Flags().StringVar(&qf.module, "module", "", "exact module match")

	addRangeFlags(searchCmd)
	searchCmd.Flags().StringVar(&qf.level, "level", "", "exact level match, e.g. error")
	searchCmd.Flags().StringVar(&qf.module, "module", "", "exact module match")
}
