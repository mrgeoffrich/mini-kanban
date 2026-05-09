package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"
)

func newRepoCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "repo", Short: "Inspect tracked repos"}
	cmd.AddCommand(repoListCmd(), repoShowCmd())
	return cmd
}

func repoListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all tracked repos",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			repos, err := c.ListRepos(context.Background())
			if err != nil {
				return err
			}
			return emit(repos)
		},
	}
}

func repoShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [PREFIX]",
		Short: "Show details for a repo (defaults to current directory)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			if len(args) == 1 {
				repo, err := c.GetRepoByPrefix(context.Background(), strings.ToUpper(args[0]))
				if err != nil {
					return err
				}
				return emit(repo)
			}
			repo, err := resolveRepoC(c)
			if err != nil {
				return err
			}
			return emit(repo)
		},
	}
}
