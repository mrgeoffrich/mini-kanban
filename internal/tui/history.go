package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"mini-kanban/internal/model"
	"mini-kanban/internal/store"
)

// historyView shows the audit log for the current repo, newest first. Each
// row is a single line; the user can scroll with j/k or page up/down.
type historyView struct {
	store *store.Store
	repo  *model.Repo

	entries []*model.HistoryEntry
	row     int
	scroll  int
	err     error
}

func newHistoryView(s *store.Store, repo *model.Repo) *historyView {
	h := &historyView{store: s, repo: repo}
	h.reload()
	return h
}

func (h *historyView) reload() {
	entries, err := h.store.ListHistory(store.HistoryFilter{
		RepoID: &h.repo.ID,
		Limit:  500,
	})
	if err != nil {
		h.err = err
		return
	}
	h.err = nil
	h.entries = entries
	if h.row >= len(entries) {
		h.row = max(0, len(entries)-1)
	}
}

func (h *historyView) HasOverlay() bool { return false }

func (h *historyView) Help() string {
	return "j/k scroll · g/G top/bottom · r reload · q quit"
}

func (h *historyView) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "j", "down":
		if h.row < len(h.entries)-1 {
			h.row++
		}
	case "k", "up":
		if h.row > 0 {
			h.row--
		}
	case "g", "home":
		h.row = 0
		h.scroll = 0
	case "G", "end":
		if len(h.entries) > 0 {
			h.row = len(h.entries) - 1
		}
	case "pgdown", " ":
		h.row += 10
		if h.row >= len(h.entries) {
			h.row = max(0, len(h.entries)-1)
		}
	case "pgup":
		h.row -= 10
		if h.row < 0 {
			h.row = 0
		}
	case "r":
		h.reload()
	}
	return nil
}

func (h *historyView) View(width, height int) string {
	if width == 0 || height == 0 {
		return ""
	}
	innerWidth := width - 2
	if innerWidth < 20 {
		innerWidth = 20
	}
	innerHeight := height - 2
	if innerHeight < 3 {
		innerHeight = 3
	}

	box := lipgloss.NewStyle().
		Border(colBorder).BorderForeground(colFocusBorder).
		Width(innerWidth).Height(innerHeight)

	if h.err != nil {
		return box.Render(errorStyle.Render(h.err.Error()))
	}
	if len(h.entries) == 0 {
		return box.Render(mutedStyle.Padding(1, 2).Render("(no history yet)"))
	}

	header := lipgloss.NewStyle().
		Bold(true).Foreground(lipgloss.Color("231")).Background(colHeaderFocus).
		Width(innerWidth).Padding(0, 1).
		Render(fmt.Sprintf("History · %d entries (newest first)", len(h.entries)))

	rowsHeight := innerHeight - 1 // header
	if rowsHeight < 1 {
		rowsHeight = 1
	}

	// Keep the cursor in view: scroll so the row sits within [scroll, scroll+rowsHeight).
	if h.row < h.scroll {
		h.scroll = h.row
	}
	if h.row >= h.scroll+rowsHeight {
		h.scroll = h.row - rowsHeight + 1
	}
	if h.scroll < 0 {
		h.scroll = 0
	}

	// Column widths inside innerWidth (account for 2 chars padding + spaces):
	// when, actor, op, target, details. Details flexes.
	const padW = 2
	whenW, actorW, opW, targetW := 16, 12, 18, 12
	usable := innerWidth - padW - whenW - actorW - opW - targetW - 4 // 4 spaces between cols
	if usable < 10 {
		usable = 10
	}

	var rows []string
	end := min(h.scroll+rowsHeight, len(h.entries))
	for i := h.scroll; i < end; i++ {
		e := h.entries[i]
		when := keyStyle.Render(formatRelative(e.CreatedAt))
		actor := truncate(e.Actor, actorW)
		op := boldStyle.Render(truncate(e.Op, opW))
		target := truncate(e.TargetLabel, targetW)
		details := truncate(oneLine(e.Details), usable)
		line := fmt.Sprintf(" %-*s %-*s %-*s %-*s %s",
			whenW, when,
			actorW, actor,
			opW, op,
			targetW, target,
			details,
		)
		// Pad/truncate the rendered line to innerWidth so highlight fills.
		styled := lipgloss.NewStyle().Width(innerWidth)
		if i == h.row {
			styled = styled.Background(cardSelectedBG).Foreground(lipgloss.Color("231"))
		}
		rows = append(rows, styled.Render(line))
	}

	body := strings.Join(rows, "\n")
	return box.Render(lipgloss.JoinVertical(lipgloss.Left, header, body))
}

// formatRelative returns short relatively-friendly timestamps. We render
// absolute timestamps for old entries because "247 days ago" is annoying.
func formatRelative(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}
