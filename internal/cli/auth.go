package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jpaddison3/betterheap/internal/client"
	"github.com/jpaddison3/betterheap/internal/config"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage Better Stack credentials",
	Long: `betterheap needs two credentials: a Telemetry API token (source discovery)
and a ClickHouse query username/password (log data). Create them in the Better
Stack dashboard under Integrations → "Connect ClickHouse HTTP client".`,
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Prompt for, validate, and store credentials",
	Args:  cobra.NoArgs,
	RunE:  runAuthLogin,
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show and validate the resolved credentials",
	Args:  cobra.NoArgs,
	RunE:  runAuthStatus,
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove stored credentials from the config file",
	Args:  cobra.NoArgs,
	RunE:  runAuthLogout,
}

func init() {
	authCmd.AddCommand(authLoginCmd, authStatusCmd, authLogoutCmd)
}

func runAuthLogin(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	token := cfg.APIToken(flagToken)
	if token == "" {
		if token, err = promptSecret("Telemetry API token: "); err != nil {
			return err
		}
	}
	quser := cfg.QueryUsername(flagQueryUser)
	if quser == "" {
		if quser, err = promptLine("ClickHouse query username: "); err != nil {
			return err
		}
	}
	qpass := cfg.QueryPassword(flagQueryPass)
	if qpass == "" {
		if qpass, err = promptSecret("ClickHouse query password: "); err != nil {
			return err
		}
	}
	region := cfg.Region(flagRegion)

	// Validate the query credentials.
	qc := client.NewQueryClient(cfg.QueryHost(""), region, quser, qpass, flagVerbose)
	if _, err := qc.QueryScalar(ctx(), "SELECT 1"); err != nil {
		return classify(fmt.Errorf("query credentials check failed: %w", err))
	}
	// Validate the Telemetry token.
	srcs, err := client.NewTelemetryClient(token, flagVerbose).ListSources(ctx())
	if err != nil {
		return classify(fmt.Errorf("Telemetry token check failed: %w", err))
	}

	cfg.File.APIToken = token
	cfg.File.QueryUsername = quser
	cfg.File.QueryPassword = qpass
	if cfg.File.Region == "" && flagRegion != "" {
		cfg.File.Region = flagRegion
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Saved credentials to %s\n", cfg.FilePath())
	fmt.Fprintf(os.Stderr, "Query credentials OK. Telemetry token OK (%d sources visible).\n", len(srcs))
	return nil
}

func runAuthStatus(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	token := cfg.APIToken(flagToken)
	quser := cfg.QueryUsername(flagQueryUser)
	qpass := cfg.QueryPassword(flagQueryPass)
	region := cfg.Region(flagRegion)

	fmt.Printf("config:    %s\n", cfg.FilePath())
	fmt.Printf("region:    %s\n", region)

	ok := true
	if quser != "" && qpass != "" {
		qc := client.NewQueryClient(cfg.QueryHost(""), region, quser, qpass, flagVerbose)
		if _, err := qc.QueryScalar(ctx(), "SELECT 1"); err != nil {
			fmt.Printf("query:     FAIL (%s): %v\n", quser, err)
			ok = false
		} else {
			fmt.Printf("query:     OK (%s)\n", quser)
		}
	} else {
		fmt.Println("query:     no credentials")
		ok = false
	}

	if token != "" {
		if srcs, err := client.NewTelemetryClient(token, flagVerbose).ListSources(ctx()); err != nil {
			fmt.Printf("telemetry: FAIL: %v\n", err)
			ok = false
		} else {
			fmt.Printf("telemetry: OK (%d sources)\n", len(srcs))
		}
	} else {
		fmt.Println("telemetry: no token")
		ok = false
	}

	if !ok {
		return exitErr{code: 2, err: errors.New("not fully authenticated (run `betterheap auth login`)")}
	}
	return nil
}

func runAuthLogout(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.File.APIToken = ""
	cfg.File.QueryUsername = ""
	cfg.File.QueryPassword = ""
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Removed stored credentials from %s\n", cfg.FilePath())
	return nil
}

func promptSecret(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return strings.TrimSpace(string(b)), err
	}
	return readLine(os.Stdin)
}

func promptLine(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	return readLine(os.Stdin)
}

func readLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", errors.New("no input provided")
	}
	return line, nil
}
