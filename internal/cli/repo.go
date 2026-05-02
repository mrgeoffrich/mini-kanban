package cli

import (
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
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			repos, err := s.ListRepos()
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
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			if len(args) == 1 {
				repo, err := s.GetRepoByPrefix(strings.ToUpper(args[0]))
				if err != nil {
					return err
				}
				return emit(repo)
			}
			repo, err := resolveRepo(s)
			if err != nil {
				return err
			}
			return emit(repo)
		},
	}
}
