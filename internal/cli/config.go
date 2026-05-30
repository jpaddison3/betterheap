package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/jpaddison3/betterheap/internal/config"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Get, set, or edit betterheap configuration",
	Long: `Defaults live in ~/.betterheap/config.json. Settable keys:
  source  region  query_host  format  since  profile  limit
  api_token  query_username  query_password (prefer 'auth login' for secrets)`,
}

var configGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Print the config file (secrets redacted)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		b, err := json.MarshalIndent(cfg.Redacted(), "", "  ")
		if err != nil {
			return err
		}
		fmt.Printf("# %s\n%s\n", cfg.FilePath(), b)
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:     "set <key> <value>",
	Short:   "Set a config value",
	Example: "  betterheap config set source myapp\n  betterheap config set format ndjson",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if err := cfg.Set(args[0], args[1]); err != nil {
			return err
		}
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "set %s in %s\n", args[0], cfg.FilePath())
		return nil
	},
}

var configEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Open the config file in $EDITOR",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		// Ensure the file exists before editing.
		if _, err := os.Stat(cfg.FilePath()); os.IsNotExist(err) {
			if err := cfg.Save(); err != nil {
				return err
			}
		}
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}
		c := exec.Command(editor, cfg.FilePath())
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		return c.Run()
	},
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the config file path",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := config.Path()
		if err != nil {
			return err
		}
		fmt.Println(p)
		return nil
	},
}

func init() {
	configCmd.AddCommand(configGetCmd, configSetCmd, configEditCmd, configPathCmd)
}
