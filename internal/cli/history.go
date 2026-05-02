package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"mini-kanban/internal/store"
)

// parseLookback extends time.ParseDuration with day (d) and week (w) units.
// Examples: 1m, 30m, 1h, 6h, 1d, 7d, 2w. The first allowable unit at the end
// of the string wins; multi-unit composites use Go's native parser.
func parseLookback(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("duration is required (e.g. 30m, 1h, 1d, 2w)")
	}
	last := s[len(s)-1]
	switch last {
	case 'd', 'w':
		n, err := strconv.Atoi(s[:len(s)-1])
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid duration %q (e.g. 1d, 2w)", s)
		}
		mult := 24 * time.Hour
		if last == 'w' {
			mult = 7 * 24 * time.Hour
		}
		return time.Duration(n) * mult, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (e.g. 30m, 1h, 1d, 2w)", s)
	}
	return d, nil
}

func newHistoryCmd() *cobra.Command {
	var (
		limit       int
		userFilter  string
		opFilter    string
		sinceStr    string
		allRepos    bool
	)
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show the audit log of mutations (newest first)",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()

			f := store.HistoryFilter{Limit: limit, Actor: userFilter, Op: opFilter}
			if !allRepos {
				repo, err := resolveRepo(s)
				if err != nil {
					return err
				}
				f.RepoID = &repo.ID
			}
			if sinceStr != "" {
				d, err := parseLookback(sinceStr)
				if err != nil {
					return err
				}
				cutoff := time.Now().Add(-d)
				f.Since = &cutoff
			}
			entries, err := s.ListHistory(f)
			if err != nil {
				return err
			}
			return emit(entries)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "max number of entries (0 for no limit)")
	cmd.Flags().StringVar(&userFilter, "user-filter", "", "filter by actor name")
	cmd.Flags().StringVar(&opFilter, "op", "", "filter by op (e.g. issue.create)")
	cmd.Flags().StringVar(&sinceStr, "since", "", "scan back this far (e.g. 30m, 1h, 1d, 2w)")
	cmd.Flags().BoolVar(&allRepos, "all-repos", false, "include entries from every repo")
	return cmd
}
