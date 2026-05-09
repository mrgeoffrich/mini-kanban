package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/mini-kanban/internal/sync"
)

// newSyncCmd registers the `mk sync` command group. Phase 3 ships
// hidden `mk sync export <path>` and `mk sync import <path>`; the
// parent group itself is hidden — these are developer/debugging
// surfaces. The user-facing `mk sync` (full pull/import/export/
// commit/push) lands in Phase 4 and that's when we'll un-hide it
// and document it in SKILL.md.
func newSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "sync",
		Short:  "Git-backed sync (developer preview)",
		Hidden: true,
	}
	cmd.AddCommand(
		newSyncExportCmd(),
	)
	return cmd
}

func newSyncExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "export <path>",
		Short:  "Export the local DB to a folder as YAML + markdown",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("mk sync export: not supported in remote mode (operates on the local DB only)")
			}
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()

			eng := &sync.Engine{
				Store:  s,
				Actor:  actor(),
				DryRun: opts.dryRun,
			}
			res, err := eng.Export(context.Background(), args[0])
			if err != nil {
				return err
			}
			out := exportResult{ExportResult: res}
			if opts.dryRun {
				return emitDryRun(out)
			}
			return emit(out)
		},
	}
	return cmd
}

// exportResult wraps *sync.ExportResult so we can register a text
// renderer in renderText without leaking sync types into the output
// package's switch. JSON output flows through ExportResult's own
// fields untouched (the embedded pointer's tags do the work).
type exportResult struct {
	*sync.ExportResult
}
