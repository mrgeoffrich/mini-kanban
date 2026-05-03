package tui

import (
	"fmt"
	"os"
	"strings"

	"mini-kanban/internal/model"
	"mini-kanban/internal/store"
)

// SnapshotOpts controls non-interactive rendering for layout debugging.
type SnapshotOpts struct {
	Target string // tab or overlay name (case-insensitive)
	Width  int
	Height int
}

// Snapshot builds the same Model the TUI uses and renders one view to
// stdout, sized to the given width/height. Lets us inspect layouts in CI,
// reproduce visual bugs, and run snapshot tests without a real terminal.
func Snapshot(s *store.Store, repo *model.Repo, opts SnapshotOpts) error {
	if opts.Width <= 0 {
		opts.Width = 120
	}
	if opts.Height <= 0 {
		opts.Height = 40
	}

	board, err := newBoardView(s, repo)
	if err != nil {
		return err
	}
	features := newFeaturesView(s, repo)
	docs := newDocsView(s, repo)
	hist := newHistoryView(s, repo)

	m := &Model{
		repo: repo,
		tabs: []tab{
			{"Board", board},
			{"Features", features},
			{"Documents", docs},
			{"History", hist},
		},
		width:  opts.Width,
		height: opts.Height,
	}

	target := strings.ToLower(strings.TrimSpace(opts.Target))
	switch target {
	case "board":
		m.active = 0
	case "features":
		m.active = 1
	case "documents", "docs":
		m.active = 2
	case "history":
		m.active = 3
	case "card-overlay", "card":
		m.active = 0
		board.overlay = true
	case "picker":
		m.active = 0
		board.openPicker()
	case "doc-overlay":
		m.active = 2
		if docs.loaded != nil {
			docs.overlay = true
		}
	case "feature-overlay":
		m.active = 1
		if features.selected != nil {
			features.overlay = true
		}
	default:
		return fmt.Errorf("unknown snapshot target %q (try board, features, docs, history, card-overlay, doc-overlay, feature-overlay, picker)", opts.Target)
	}

	out := m.View()
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	_, err = os.Stdout.WriteString(out)
	return err
}
