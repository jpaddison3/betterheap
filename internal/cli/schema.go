package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jpaddison3/betterheap/internal/envelope"
)

type schemaProfile struct {
	Name   string            `json:"name"`
	Fields map[string]string `json:"fields"`
}

type schemaDoc struct {
	Profiles             []schemaProfile     `json:"profiles"`
	CommandDefaultFields map[string][]string `json:"command_default_fields"`
	ExitCodes            map[int]string      `json:"exit_codes"`
}

var schemaCmd = &cobra.Command{
	Use:   "schema",
	Short: "Dump the field map, command output fields, and exit codes as JSON",
	Long:  "Machine-readable description of betterheap's envelope field mappings and conventions, for agents configuring themselves.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		doc := schemaDoc{
			CommandDefaultFields: map[string][]string{
				"logs":     defaultLogFields,
				"tail":     defaultLogFields,
				"errors":   defaultLevelFields,
				"warnings": defaultLevelFields,
				"search":   defaultLogFields,
			},
			ExitCodes: map[int]string{
				0: "ok",
				1: "query error",
				2: "auth error",
				3: "no results",
				4: "partial tier/source",
			},
		}
		for _, name := range envelope.Names() {
			p, _ := envelope.Get(name)
			doc.Profiles = append(doc.Profiles, schemaProfile{Name: name, Fields: p.Map()})
		}
		b, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return err
		}
		fmt.Printf("%s\n", b)
		return nil
	},
}
