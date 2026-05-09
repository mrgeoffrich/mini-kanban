package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/mini-kanban/internal/model"
	"github.com/mrgeoffrich/mini-kanban/internal/store"
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
		newSyncImportCmd(),
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

// newSyncImportCmd is the Phase-3 inbound counterpart to `mk sync
// export`. Reads `<path>` (a sync-repo working tree) and applies it
// to the local DB through the four-phase import pipeline. Honours
// `--dry-run` (rolls back the outer transaction) and `--user`
// (audit ops attribute the work).
//
// Hidden in the same spirit as `mk sync export`: this is a Phase-3
// development surface; the user-facing `mk sync` (full
// pull/import/export/commit/push) lands in Phase 4.
func newSyncImportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "import <path>",
		Short:  "Import a sync-repo folder into the local DB",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("mk sync import: not supported in remote mode (operates on the local DB only)")
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
			res, err := eng.Import(context.Background(), args[0])
			if err != nil {
				return err
			}
			recordSyncImportOps(s, res)
			out := importResult{ImportResult: res}
			if opts.dryRun {
				return emitDryRun(out)
			}
			return emit(out)
		},
	}
	return cmd
}

// importResult mirrors exportResult — a thin wrapper so renderText
// can dispatch on the type without leaking sync.* into the output
// package.
type importResult struct {
	*sync.ImportResult
}

// recordSyncImportOps writes audit-log rows for the side effects of
// an import: one sync.import per invocation, plus per-renumber,
// per-rename, and per-deletion entries. Failures don't fail the
// command (recordOp logs to stderr).
func recordSyncImportOps(s *store.Store, res *sync.ImportResult) {
	if res == nil {
		return
	}
	if opts.dryRun {
		return // dry-run leaves no audit trail; mirrors the rest of mk
	}
	details := fmt.Sprintf("repos=%d issues=%d features=%d documents=%d comments=%d inserted=%d updated=%d noop=%d",
		res.Repos, res.Issues, res.Features, res.Documents, res.Comments,
		res.Inserted, res.Updated, res.NoOp)
	recordOp(s, model.HistoryEntry{
		Op:      "sync.import",
		Kind:    "repo",
		Details: details,
	})
	for _, r := range res.Renumbered {
		recordOp(s, model.HistoryEntry{
			Op:          "sync.renumber",
			Kind:        "issue",
			TargetLabel: fmt.Sprintf("%s-%d", r.Prefix, r.NewNumber),
			Details:     fmt.Sprintf("from %s-%d (uuid=%s)", r.Prefix, r.OldNumber, r.UUID),
		})
	}
	for _, r := range res.Renamed {
		recordOp(s, model.HistoryEntry{
			Op:          "sync.rename",
			Kind:        r.Kind,
			TargetLabel: r.New,
			Details:     fmt.Sprintf("from %s (uuid=%s)", r.Old, r.UUID),
		})
	}
	for _, d := range res.Deleted {
		recordOp(s, model.HistoryEntry{
			Op:          "sync.delete",
			Kind:        d.Kind,
			TargetLabel: d.Label,
			Details:     fmt.Sprintf("uuid=%s", d.UUID),
		})
	}
}
