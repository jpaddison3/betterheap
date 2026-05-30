// Package cli wires betterheap's cobra command tree, global flags, and the
// shared application environment (config + HTTP clients + routing engine).
package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/jpaddison3/betterheap/internal/client"
	"github.com/jpaddison3/betterheap/internal/config"
	"github.com/jpaddison3/betterheap/internal/routing"
	"github.com/jpaddison3/betterheap/internal/source"
)

// Global flags (persistent on root), mirroring dharma's spine.
var (
	flagToken      string
	flagQueryUser  string
	flagQueryPass  string
	flagSource     string
	flagRegion     string
	flagFormat     string
	flagProfile    string
	flagVerbose    bool
	flagNoColor    bool
	flagConfigPath string
)

var rootCmd = &cobra.Command{
	Use:   "betterheap",
	Short: "Agent-friendly CLI for Better Stack logs across the live buffer and S3 archive",
	Long: `betterheap queries Better Stack Telemetry logs as one continuous window
across the live hot buffer and the S3 archive — so the time range you ask for is
the time range you get, not a silent ~35-minute cap.

It understands the JSON log envelope, so level/message/module are first-class:

  betterheap errors --since 7d                  # spans the archive automatically
  betterheap search "Failed to truncate" --since 14d
  betterheap logs --since 2026-05-28 --level error --module jobRecommender
  betterheap tail -f                            # follow the live buffer

Credentials resolve from flags, then BETTERHEAP_* env, then BETTERSTACK_* env,
then ~/.betterheap/config.json, then bslog's ~/.bslog/env — so existing bslog
users need no setup. Run 'betterheap auth login' to store them natively.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVar(&flagToken, "token", "", "Telemetry API token (env: BETTERHEAP_API_TOKEN)")
	pf.StringVar(&flagQueryUser, "query-user", "", "ClickHouse query username (env: BETTERHEAP_QUERY_USERNAME)")
	pf.StringVar(&flagQueryPass, "query-pass", "", "ClickHouse query password (env: BETTERHEAP_QUERY_PASSWORD)")
	pf.StringVarP(&flagSource, "source", "s", "", "source name (env: BETTERHEAP_SOURCE)")
	pf.StringVar(&flagRegion, "region", "", "Better Stack region, e.g. eu-nbg-2 (env: BETTERHEAP_REGION)")
	pf.StringVar(&flagFormat, "format", "", "output format: json|ndjson|table|csv|pretty (default: pretty on a TTY, ndjson piped)")
	pf.StringVar(&flagProfile, "profile", "", "envelope profile: vercel|render|raw (default: auto-detect from source)")
	pf.BoolVarP(&flagVerbose, "verbose", "v", false, "log HTTP requests to stderr")
	pf.BoolVar(&flagNoColor, "no-color", false, "disable color (also honors NO_COLOR)")
	pf.StringVar(&flagConfigPath, "config", "", "config file path (default: ~/.betterheap/config.json)")

	rootCmd.AddCommand(
		authCmd,
		configCmd,
		sourcesCmd,
		sqlCmd,
		logsCmd,
		tailCmd,
		errorsCmd,
		warningsCmd,
		searchCmd,
		statsCmd,
		traceCmd,
		schemaCmd,
		versionCmd,
	)
}

// Execute runs the CLI and maps errors to betterheap's exit-code scheme.
func Execute() {
	err := rootCmd.Execute()
	if err == nil {
		return
	}
	var ee exitErr
	if asExit(err, &ee) {
		if ee.err != nil {
			fmt.Fprintln(os.Stderr, "error:", ee.err)
		}
		os.Exit(ee.code)
	}
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

// appEnv bundles resolved config and the clients commands need.
type appEnv struct {
	cfg      *config.Store
	qc       *client.QueryClient
	tel      *client.TelemetryClient
	reg      *source.Registry
	eng      *routing.Engine
	cacheDir string
}

// newAppEnv resolves credentials/config and constructs the clients.
func newAppEnv() (*appEnv, error) {
	if flagConfigPath != "" {
		_ = os.Setenv("BETTERHEAP_CONFIG", flagConfigPath)
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	region := cfg.Region(flagRegion)
	qc := client.NewQueryClient(cfg.QueryHost(""), region, cfg.QueryUsername(flagQueryUser), cfg.QueryPassword(flagQueryPass), flagVerbose)
	tel := client.NewTelemetryClient(cfg.APIToken(flagToken), flagVerbose)
	cacheDir := filepath.Join(filepath.Dir(cfg.FilePath()), "cache")
	return &appEnv{
		cfg:      cfg,
		qc:       qc,
		tel:      tel,
		reg:      source.NewRegistry(tel, region, cacheDir),
		eng:      routing.NewEngine(qc, cacheDir),
		cacheDir: cacheDir,
	}, nil
}

func ctx() context.Context { return context.Background() }
