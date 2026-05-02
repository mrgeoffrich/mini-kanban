package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"mini-kanban/internal/git"
	"mini-kanban/internal/model"
	"mini-kanban/internal/store"
)

func newInitCmd() *cobra.Command {
	var prefix string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Bind the current git repo to a kanban prefix",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			info, err := git.Detect(cwd)
			if err != nil {
				return err
			}

			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()

			if existing, err := s.GetRepoByPath(info.Root); err == nil {
				return ok("repo already initialised as %s (%s)", existing.Prefix, existing.Path)
			} else if !errors.Is(err, store.ErrNotFound) {
				return err
			}

			var chosen string
			if prefix != "" {
				p, err := store.ValidatePrefix(prefix)
				if err != nil {
					return err
				}
				if existing, err := s.GetRepoByPrefix(p); err == nil {
					return fmt.Errorf("prefix %s already in use by %s", p, existing.Path)
				}
				chosen = p
			} else {
				chosen, err = s.AllocatePrefix(info.Name)
				if err != nil {
					return err
				}
			}

			repo, err := s.CreateRepo(chosen, info.Name, info.Root, info.RemoteURL)
			if err != nil {
				return err
			}
			recordOp(s, model.HistoryEntry{
				RepoID: &repo.ID, RepoPrefix: repo.Prefix,
				Op: "repo.create", Kind: "repo",
				TargetID: &repo.ID, TargetLabel: repo.Prefix,
				Details: "explicit init (" + repo.Name + ")",
			})
			return emit(repo)
		},
	}
	cmd.Flags().StringVar(&prefix, "prefix", "", "explicit 4-char prefix (e.g. AUTH)")
	return cmd
}
