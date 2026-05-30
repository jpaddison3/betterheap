package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version is the betterheap build version (overridable via -ldflags).
var version = "0.1.0-dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the betterheap version",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(version)
	},
}
