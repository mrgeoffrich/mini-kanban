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
// Examples: 30m, 1h, 6h, 1d, 7d, 2w. Single-unit only for d/w; multi-unit
// composites use Go's native parser.
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

// parseTimestamp accepts several common shapes and returns a UTC time.
// Date-only and bare datetimes are interpreted in the user's local
// timezone; RFC 3339 inputs honour the offset they specify.
//
// Accepted: 2006-01-02 | 2006-01-02 15:04 | 2006-01-02 15:04:05 |
//
//	2006-01-02T15:04:05 | RFC3339 (e.g. 2026-05-03T07:27:14Z).
func parseTimestamp(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("timestamp is required")
	}
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid timestamp %q (try YYYY-MM-DD, YYYY-MM-DD HH:MM, or RFC 3339)", s)
}

func newHistoryCmd() *cobra.Command {
	var (
		limit       int
		offset      int
		userFilter  string
		opFilter    string
		kindFilter  string
		sinceStr    string
		fromStr     string
		toStr       string
		allRepos    bool
		oldestFirst bool
	)
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show the audit log of mutations",
		Long: `By default, lists the most recent mutations in the current repo
(newest first). All filters can be combined freely except --since,
which is mutually exclusive with --from (it's just sugar for
--from = now - duration).

Time inputs:
  --since accepts durations: 30m, 1h, 1d, 2w
  --from / --to accept either local-time stamps (YYYY-MM-DD,
  YYYY-MM-DD HH:MM, YYYY-MM-DD HH:MM:SS) or RFC 3339 (Z-suffixed
  UTC). Bare dates start at 00:00 in the local timezone.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sinceStr != "" && fromStr != "" {
				return fmt.Errorf("--since and --from are mutually exclusive")
			}
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()

			f := store.HistoryFilter{
				Limit:       limit,
				Offset:      offset,
				Actor:       userFilter,
				Op:          opFilter,
				Kind:        kindFilter,
				OldestFirst: oldestFirst,
			}
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
				f.From = &cutoff
			}
			if fromStr != "" {
				t, err := parseTimestamp(fromStr)
				if err != nil {
					return fmt.Errorf("--from: %w", err)
				}
				f.From = &t
			}
			if toStr != "" {
				t, err := parseTimestamp(toStr)
				if err != nil {
					return fmt.Errorf("--to: %w", err)
				}
				f.To = &t
			}
			if f.From != nil && f.To != nil && f.To.Before(*f.From) {
				return fmt.Errorf("--to (%s) must not be before --from (%s)", f.To.Local(), f.From.Local())
			}
			entries, err := s.ListHistory(f)
			if err != nil {
				return err
			}
			return emit(entries)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "max number of entries (0 for no limit)")
	cmd.Flags().IntVar(&offset, "offset", 0, "skip the first N entries (for pagination)")
	cmd.Flags().StringVar(&userFilter, "user-filter", "", "filter by actor name")
	cmd.Flags().StringVar(&opFilter, "op", "", "filter by op (e.g. issue.create)")
	cmd.Flags().StringVar(&kindFilter, "kind", "", "filter by kind (issue, feature, document, repo)")
	cmd.Flags().StringVar(&sinceStr, "since", "", "look back this far (e.g. 30m, 1h, 1d, 2w); shorthand for --from")
	cmd.Flags().StringVar(&fromStr, "from", "", "include entries on/after this timestamp")
	cmd.Flags().StringVar(&toStr, "to", "", "include entries on/before this timestamp")
	cmd.Flags().BoolVar(&allRepos, "all-repos", false, "include entries from every repo")
	cmd.Flags().BoolVar(&oldestFirst, "oldest-first", false, "sort oldest first instead of newest first")
	return cmd
}
