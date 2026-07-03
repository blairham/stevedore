package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/jsonschema"
	"github.com/spf13/cobra"
)

func newSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Print the JSON Schema for .stevedore.yaml",
		Long: "schema emits a JSON Schema for the stevedore config. Save it and point\n" +
			"your editor at it for autocomplete and validation:\n\n" +
			"  stevedore schema > stevedore.schema.json\n\n" +
			"then add to the top of .stevedore.yaml:\n\n" +
			"  # yaml-language-server: $schema=./stevedore.schema.json",
		RunE: func(_ *cobra.Command, _ []string) error {
			s := jsonschema.Generate(config.Config{}, "stevedore config")
			data, err := json.MarshalIndent(s, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(data))
			return nil
		},
	}
}
