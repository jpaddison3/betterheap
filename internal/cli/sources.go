package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jpaddison3/betterheap/internal/client"
	"github.com/jpaddison3/betterheap/internal/output"
)

var sourcesRefresh bool

var sourcesCmd = &cobra.Command{
	Use:   "sources",
	Short: "List and inspect Better Stack sources",
}

var sourcesListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List sources with platform, profile, region, and retention",
	Args:    cobra.NoArgs,
	Example: "  betterheap sources list\n  betterheap sources list --format json",
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := newAppEnv()
		if err != nil {
			return err
		}
		list := a.reg.List
		if sourcesRefresh {
			list = a.reg.Refresh
		}
		sources, err := list(ctx())
		if err != nil {
			return classify(err)
		}

		isTTY := output.IsTTY(os.Stdout)
		format, err := output.ResolveFormat(a.cfg.Format(flagFormat), isTTY)
		if err != nil {
			return err
		}
		fields := []string{"name", "platform", "profile", "region", "retention_d", "paused"}
		sink := output.NewSink(os.Stdout, format, fields, output.ColorEnabled(flagNoColor, isTTY))
		for _, s := range sources {
			if err := sink.Write(client.Row{
				"name":        s.Name,
				"platform":    s.Platform,
				"profile":     s.Profile,
				"region":      s.Region,
				"retention_d": s.LogsRetention,
				"paused":      s.IngestingPaused,
			}); err != nil {
				return err
			}
		}
		if err := sink.Close(); err != nil {
			return err
		}
		if len(sources) == 0 {
			return exitErr{code: 3}
		}
		return nil
	},
}

type tierStats struct {
	Oldest string `json:"oldest"`
	Newest string `json:"newest"`
	Count  string `json:"count"`
	Error  string `json:"error,omitempty"`
}

type sourceShow struct {
	Name              string    `json:"name"`
	Display           string    `json:"display"`
	Platform          string    `json:"platform"`
	Profile           string    `json:"profile"`
	Region            string    `json:"region"`
	TeamID            int       `json:"team_id"`
	LiveTable         string    `json:"live_table"`
	ArchiveTable      string    `json:"archive_table"`
	LogsRetentionDays int       `json:"logs_retention_days"`
	IngestingPaused   bool      `json:"ingesting_paused"`
	Live              tierStats `json:"live"`
	Archive           tierStats `json:"archive"`
}

var sourcesShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show table ids, region, profile, and tier ranges for a source",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := newAppEnv()
		if err != nil {
			return err
		}
		src, _, err := a.resolveSource(args[0])
		if err != nil {
			return classify(err)
		}

		show := sourceShow{
			Name:              src.Name,
			Display:           src.Display,
			Platform:          src.Platform,
			Profile:           src.Profile,
			Region:            src.Region,
			TeamID:            src.TeamID,
			LiveTable:         src.LiveTable(),
			ArchiveTable:      src.ArchiveTable(),
			LogsRetentionDays: src.LogsRetention,
			IngestingPaused:   src.IngestingPaused,
		}
		show.Live = probe(a.qc, fmt.Sprintf("SELECT min(dt) AS lo, max(dt) AS hi, count() AS n FROM remote(%s)", src.LiveTable()))
		show.Archive = probe(a.qc, fmt.Sprintf("SELECT min(dt) AS lo, max(dt) AS hi, count() AS n FROM s3Cluster(primary, %s) WHERE _row_type = 1", src.ArchiveTable()))

		format, err := output.ResolveFormat(a.cfg.Format(flagFormat), output.IsTTY(os.Stdout))
		if err != nil {
			return err
		}
		if format == output.FormatJSON || format == output.FormatNDJSON {
			b, _ := json.MarshalIndent(show, "", "  ")
			fmt.Printf("%s\n", b)
			return nil
		}
		printSourceShow(show)
		return nil
	},
}

// probe runs a min/max/count query against one tier, tolerating failure.
func probe(qc *client.QueryClient, sql string) tierStats {
	rows, err := qc.Query(ctx(), sql)
	if err != nil {
		return tierStats{Error: err.Error()}
	}
	if len(rows) == 0 {
		return tierStats{}
	}
	r := rows[0]
	get := func(k string) string {
		if v, ok := r[k]; ok && v != nil {
			return fmt.Sprint(v)
		}
		return ""
	}
	return tierStats{Oldest: get("lo"), Newest: get("hi"), Count: get("n")}
}

func printSourceShow(s sourceShow) {
	fmt.Printf("name:          %s\n", s.Name)
	if s.Display != "" && s.Display != s.Name {
		fmt.Printf("display:       %s\n", s.Display)
	}
	fmt.Printf("platform:      %s\n", s.Platform)
	fmt.Printf("profile:       %s\n", s.Profile)
	fmt.Printf("region:        %s\n", s.Region)
	fmt.Printf("live table:    remote(%s)\n", s.LiveTable)
	fmt.Printf("archive table: s3Cluster(primary, %s) WHERE _row_type=1\n", s.ArchiveTable)
	fmt.Printf("retention:     %d days\n", s.LogsRetentionDays)
	if s.IngestingPaused {
		fmt.Printf("ingesting:     PAUSED\n")
	}
	printTier("live", s.Live)
	printTier("archive", s.Archive)
}

func printTier(name string, t tierStats) {
	if t.Error != "" {
		fmt.Printf("%-8s      error: %s\n", name+":", t.Error)
		return
	}
	fmt.Printf("%-8s      %s → %s  (%s rows)\n", name+":", t.Oldest, t.Newest, t.Count)
}

func init() {
	sourcesListCmd.Flags().BoolVar(&sourcesRefresh, "refresh", false, "bypass the cache and re-fetch from the Telemetry API")
	sourcesCmd.AddCommand(sourcesListCmd, sourcesShowCmd)
}
