package cli

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jpaddison3/betterheap/internal/envelope"
	"github.com/jpaddison3/betterheap/internal/output"
	"github.com/jpaddison3/betterheap/internal/routing"
	"github.com/jpaddison3/betterheap/internal/source"
)

var sqlExplain bool

// hasFormat detects a trailing FORMAT clause so we don't append a second one.
var hasFormat = regexp.MustCompile(`(?i)\bFORMAT\s+[A-Za-z0-9_]+\s*;?\s*$`)

// templateToken matches {logs}, {message}, etc.
var templateToken = regexp.MustCompile(`\{(\w+)\}`)

var sqlCmd = &cobra.Command{
	Use:   "sql <query>",
	Short: "Run a raw ClickHouse query, with optional tier/envelope template tokens",
	Long: `Sends SQL to the Query API. Reference the tiers directly:

  live buffer:  remote(t<team>_<source>_logs)
  archive:      s3Cluster(primary, t<team>_<source>_s3) WHERE _row_type = 1

Or use template tokens that expand for the active source (-s) and window:

  {logs}    tier-routed relation for --since/--until (live, archive, or both)
  {live}    remote(<live table>)        {archive}  s3Cluster(primary, <archive table>)
  {message} {level} {module} {env} {status} {request_id} {path} {dt}

  betterheap sql "SELECT dt, {message} AS msg FROM {logs} WHERE {level}='error' ORDER BY dt DESC LIMIT 50" --since 7d

A FORMAT clause matching --format is appended unless your query already has one.`,
	Example: `  betterheap sql "SELECT count() FROM {archive} WHERE _row_type=1 AND dt >= '2026-05-01'"
  betterheap sql "SELECT {level} AS lvl, count() FROM {logs} GROUP BY lvl" --since 7d`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := newAppEnv()
		if err != nil {
			return err
		}
		sql := strings.TrimSpace(args[0])

		if templateToken.MatchString(sql) {
			src, prof, plan, err := a.routeFor(flagSource, "")
			if err != nil {
				return err
			}
			if sql, err = expandTemplate(sql, src, prof, plan); err != nil {
				return err
			}
		}

		format, err := output.ResolveFormat(a.cfg.Format(flagFormat), output.IsTTY(os.Stdout))
		if err != nil {
			return err
		}
		final := sql
		if !hasFormat.MatchString(sql) {
			final = strings.TrimRight(sql, ";") + "\nFORMAT " + clickhouseFormat(format)
		}
		if sqlExplain {
			fmt.Fprintln(os.Stderr, final)
		}
		b, err := a.qc.RawBytes(ctx(), final)
		if err != nil {
			return classify(err)
		}
		os.Stdout.Write(b)
		if len(b) > 0 && b[len(b)-1] != '\n' {
			fmt.Fprintln(os.Stdout)
		}
		return nil
	},
}

// expandTemplate replaces {…} tokens with tier-routed relations and envelope
// expressions. Unknown tokens are an error to catch typos early.
func expandTemplate(sql string, src *source.Source, prof envelope.Profile, plan *routing.Plan) (string, error) {
	repl := map[string]string{
		"live":    fmt.Sprintf("remote(%s)", src.LiveTable()),
		"archive": fmt.Sprintf("s3Cluster(primary, %s)", src.ArchiveTable()),
		"logs":    logsRelation(plan),
	}
	for _, f := range []string{"message", "level", "module", "env", "status", "request_id", "path", "dt"} {
		if e, ok := prof.Expr(f); ok {
			repl[f] = e
		}
	}
	for _, m := range templateToken.FindAllStringSubmatch(sql, -1) {
		if _, ok := repl[m[1]]; !ok {
			return "", fmt.Errorf("unknown template token {%s}", m[1])
		}
	}
	return templateToken.ReplaceAllStringFunc(sql, func(tok string) string {
		return repl[tok[1:len(tok)-1]]
	}), nil
}

// logsRelation renders the routed tiers as a parenthesized relation usable in a
// FROM clause, preserving each tier's dt/_row_type predicates. It projects only
// dt and raw — the columns the envelope tokens operate on — because the live
// and archive tables have different physical schemas, so SELECT * can't UNION.
func logsRelation(plan *routing.Plan) string {
	parts := make([]string, 0, len(plan.Tiers))
	for _, t := range plan.Tiers {
		where := ""
		if len(t.Where) > 0 {
			where = " WHERE " + strings.Join(t.Where, " AND ")
		}
		parts = append(parts, fmt.Sprintf("SELECT dt, raw FROM %s%s", t.Table, where))
	}
	return "(" + strings.Join(parts, " UNION ALL ") + ")"
}

// clickhouseFormat maps a betterheap format to a native ClickHouse FORMAT.
func clickhouseFormat(f output.Format) string {
	switch f {
	case output.FormatJSON:
		return "JSON"
	case output.FormatCSV:
		return "CSVWithNames"
	case output.FormatNDJSON:
		return "JSONEachRow"
	default: // table, pretty
		return "PrettyCompact"
	}
}

func init() {
	sqlCmd.Flags().BoolVar(&sqlExplain, "explain", false, "print the final SQL (expanded, with FORMAT) to stderr")
	f := sqlCmd.Flags()
	f.StringVar(&qf.since, "since", "", "window start for {logs}: 7d, 24h, 2026-05-28")
	f.StringVar(&qf.until, "until", "", "window end for {logs}")
	f.StringVar(&qf.tier, "tier", "", "force tier for {logs}: auto|live|archive|both")
	f.BoolVar(&qf.live, "live", false, "shortcut for --tier live")
	f.BoolVar(&qf.allTime, "all-time", false, "permit an unbounded archive {logs}")
}
