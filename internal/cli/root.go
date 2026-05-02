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
}

var opts = globalOpts{output: outputText}

func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "mk",
		Short:         "mini-kanban: a Linear-style issue tracker, CLI-first",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().VarP(newOutputFlag(&opts.output), "output", "o", "output format: text|json")
	root.PersistentFlags().StringVar(&opts.dbPath, "db", "", "override database path (default: ~/.mini-kanban/db.sqlite)")

	root.AddCommand(
		newInitCmd(),
		newRepoCmd(),
		newFeatureCmd(),
		newIssueCmd(),
		newCommentCmd(),
		newLinkCmd(),
		newUnlinkCmd(),
		newPRCmd(),
		newAttachCmd(),
	)
	return root
}
