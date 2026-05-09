package cli

import (
	"github.com/spf13/cobra"
)

type outputFormat string

const (
	outputText outputFormat = "text"
	outputJSON outputFormat = "json"
)

type globalOpts struct {
	output outputFormat
	dbPath string
	user   string
	dryRun bool
}

var opts = globalOpts{output: outputText}

func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "mk",
		Short:         "mini-kanban: a local-first issue tracker, CLI-first",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return validateActorFlag()
		},
	}
	root.PersistentFlags().VarP(newOutputFlag(&opts.output), "output", "o", "output format: text|json")
	root.PersistentFlags().StringVar(&opts.dbPath, "db", "", "override database path (default: ~/.mini-kanban/db.sqlite)")
	root.PersistentFlags().StringVar(&opts.user, "user", "", "actor name recorded in history (defaults to OS user; AI agents must pass this explicitly)")
	root.PersistentFlags().BoolVar(&opts.dryRun, "dry-run", false, "validate the request and emit the projected result without writing to the database (no audit log entry)")

	root.AddCommand(
		newInitCmd(),
		newRepoCmd(),
		newFeatureCmd(),
		newIssueCmd(),
		newCommentCmd(),
		newLinkCmd(),
		newUnlinkCmd(),
		newPRCmd(),
		newTagCmd(),
		newDocCmd(),
		newStatusCmd(),
		newHistoryCmd(),
		newSchemaCmd(),
		newAPICmd(),
		newInstallSkillCmd(),
		newTUICmd(),
	)
	return root
}
