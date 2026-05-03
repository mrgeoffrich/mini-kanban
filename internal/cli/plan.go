package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/mini-kanban/internal/model"
	"github.com/mrgeoffrich/mini-kanban/internal/store"
)

func featurePlanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "plan <slug>",
		Short: "Print feature issues in execution order, respecting `blocks` dependencies",
		Long: `Compute a topological order over the open issues in a feature, using the
'blocks' relation to determine dependencies. Issues with all blockers satisfied
appear first; issues blocked by other open work appear after their blockers.

Open issues are anything not in done / cancelled / duplicate. Cross-feature
blockers (open issues outside this feature) are surfaced as blocked_by hints
but cannot be ordered against in-feature issues, so they don't gate the topo
position.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			repo, err := resolveRepo(s)
			if err != nil {
				return err
			}
			f, err := s.GetFeatureBySlug(repo.ID, args[0])
			if err != nil {
				return err
			}
			issues, err := s.ListIssues(store.IssueFilter{RepoID: &repo.ID, FeatureID: &f.ID})
			if err != nil {
				return err
			}
			view, err := buildPlan(s, f, issues)
			if err != nil {
				return err
			}
			return emit(view)
		},
	}
}

type planEntry struct {
	Key       string      `json:"key"`
	Title     string      `json:"title"`
	State     model.State `json:"state"`
	Assignee  string      `json:"assignee,omitempty"`
	BlockedBy []string    `json:"blocked_by,omitempty"`
}

type planView struct {
	Feature string      `json:"feature"`
	Order   []planEntry `json:"order"`
}

func isOpenState(s model.State) bool {
	switch s {
	case model.StateDone, model.StateCancelled, model.StateDuplicate:
		return false
	}
	return true
}

func buildPlan(s *store.Store, f *model.Feature, all []*model.Issue) (*planView, error) {
	open := make([]*model.Issue, 0, len(all))
	for _, iss := range all {
		if isOpenState(iss.State) {
			open = append(open, iss)
		}
	}
	sort.SliceStable(open, func(i, j int) bool {
		if open[i].RepoID != open[j].RepoID {
			return open[i].RepoID < open[j].RepoID
		}
		return open[i].Number < open[j].Number
	})

	ids := make([]int64, len(open))
	inSet := make(map[int64]bool, len(open))
	for i, iss := range open {
		ids[i] = iss.ID
		inSet[iss.ID] = true
	}
	blockers, err := s.BlockersFor(ids)
	if err != nil {
		return nil, err
	}

	inDeg := make(map[int64]int, len(open))
	forward := make(map[int64][]int64) // blocker id -> [blocked id], both in-set
	for _, iss := range open {
		inDeg[iss.ID] = 0
	}
	for blockedID, bs := range blockers {
		for _, b := range bs {
			if !isOpenState(b.BlockerState) {
				continue
			}
			if !inSet[b.BlockerID] {
				continue // external blocker — display only, not in topo
			}
			inDeg[blockedID]++
			forward[b.BlockerID] = append(forward[b.BlockerID], blockedID)
		}
	}

	order := make([]*model.Issue, 0, len(open))
	remaining := open
	for len(remaining) > 0 {
		var next []*model.Issue
		var processed []int64
		for _, iss := range remaining {
			if inDeg[iss.ID] == 0 {
				order = append(order, iss)
				processed = append(processed, iss.ID)
			} else {
				next = append(next, iss)
			}
		}
		if len(processed) == 0 {
			keys := make([]string, len(remaining))
			for i, iss := range remaining {
				keys[i] = iss.Key
			}
			return nil, fmt.Errorf("dependency cycle among: %s", strings.Join(keys, ", "))
		}
		for _, id := range processed {
			for _, b := range forward[id] {
				inDeg[b]--
			}
		}
		remaining = next
	}

	view := &planView{Feature: f.Slug, Order: make([]planEntry, 0, len(order))}
	for _, iss := range order {
		var by []string
		for _, b := range blockers[iss.ID] {
			if !isOpenState(b.BlockerState) {
				continue
			}
			by = append(by, b.BlockerKey)
		}
		sort.Strings(by)
		view.Order = append(view.Order, planEntry{
			Key:       iss.Key,
			Title:     iss.Title,
			State:     iss.State,
			Assignee:  iss.Assignee,
			BlockedBy: by,
		})
	}
	return view, nil
}
