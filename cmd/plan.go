package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/blairham/stevedore/internal/pipeline"
	"github.com/spf13/cobra"
)

func newPlanCmd() *cobra.Command {
	var (
		onlyChanged  bool
		changedSince string
		snapshot     bool
	)
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Resolve versions and change detection; print the build plan as JSON",
		Long: "plan decides everything a release run would — per-image versions, change\n" +
			"detection (marker refs, --changed-since, or --only-changed), and build-once\n" +
			"grouping — and prints it as JSON without building anything. The `include`\n" +
			"array is GitHub Actions matrix shape: fan one CI job out per entry and run\n" +
			"`stevedore release --only <entry.only> <entry.pins>` in each.",
		RunE: func(_ *cobra.Command, _ []string) error {
			o, err := baseOptions()
			if err != nil {
				return err
			}
			o.OnlyChanged = onlyChanged
			o.ChangedSince = changedSince
			o.Snapshot = snapshot
			result, err := pipeline.Plan(o)
			if err != nil {
				return err
			}
			// Compact, single-line JSON: the consumer is CI (`fromJson`,
			// `$GITHUB_OUTPUT`); pipe through jq for a human view.
			data, err := json.Marshal(result)
			if err != nil {
				return err
			}
			fmt.Println(string(data))
			return nil
		},
	}
	cmd.Flags().BoolVar(&onlyChanged, "only-changed", false, "skip images whose build inputs are unchanged since the last release (fingerprint state)")
	cmd.Flags().StringVar(&changedSince, "changed-since", "", "git ref: plan only images whose paths changed since this ref")
	cmd.Flags().BoolVar(&snapshot, "snapshot", false, "plan a snapshot release (affects floating tags and versioning)")
	return cmd
}
