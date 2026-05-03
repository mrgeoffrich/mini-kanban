package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"mini-kanban/internal/model"
	"mini-kanban/internal/store"
)

// boardView is the kanban tab: one column per state, optional bottom detail
// pane, and a fullscreen card overlay opened with `enter`.
type boardView struct {
	store         *store.Store
	repo          *model.Repo
	states        []model.State
	hidden        map[model.State]bool
	columns       map[model.State][]*model.Issue
	col           int
	rows          map[model.State]int
	detailVisible bool

	selected    *model.Issue
	comments    []*model.Comment
	commentsErr error

	overlay       bool
	overlayScroll int

	picker    bool
	pickerRow int

	err error
}

func newBoardView(s *store.Store, repo *model.Repo) (*boardView, error) {
	hidden, err := s.LoadHiddenStates(repo.ID)
	if err != nil {
		return nil, err
	}
	b := &boardView{
		store:         s,
		repo:          repo,
		states:        model.AllStates(),
		hidden:        hidden,
		columns:       map[model.State][]*model.Issue{},
		rows:          map[model.State]int{},
		detailVisible: true,
	}
	if err := b.reload(); err != nil {
		return nil, err
	}
	b.clampCol()
	b.refreshSelection()
	return b, nil
}

// visibleStates returns the states whose columns should be drawn, preserving
// canonical state order. When everything is hidden the slice is empty and the
// caller renders a placeholder.
func (b *boardView) visibleStates() []model.State {
	out := make([]model.State, 0, len(b.states))
	for _, st := range b.states {
		if !b.hidden[st] {
			out = append(out, st)
		}
	}
	return out
}

// clampCol pulls b.col back into range whenever the visible set changes.
func (b *boardView) clampCol() {
	v := b.visibleStates()
	if len(v) == 0 {
		b.col = 0
		return
	}
	if b.col >= len(v) {
		b.col = len(v) - 1
	}
	if b.col < 0 {
		b.col = 0
	}
}

// persistHidden writes the current hidden set to disk. Errors are stored on
// the model so the user sees them in the footer instead of crashing the UI.
func (b *boardView) persistHidden() {
	if err := b.store.SaveHiddenStates(b.repo.ID, b.hidden); err != nil {
		b.err = err
	}
}

func (b *boardView) reload() error {
	issues, err := b.store.ListIssues(store.IssueFilter{RepoID: &b.repo.ID})
	if err != nil {
		return err
	}
	for _, st := range b.states {
		b.columns[st] = nil
	}
	for _, iss := range issues {
		b.columns[iss.State] = append(b.columns[iss.State], iss)
	}
	return nil
}

func (b *boardView) currentIssue() *model.Issue {
	v := b.visibleStates()
	if b.col < 0 || b.col >= len(v) {
		return nil
	}
	st := v[b.col]
	issues := b.columns[st]
	if len(issues) == 0 {
		return nil
	}
	r := b.rows[st]
	if r >= len(issues) {
		r = len(issues) - 1
	}
	if r < 0 {
		r = 0
	}
	return issues[r]
}

// refreshSelection re-fetches comments only when the selected issue changes
// — keeps navigation snappy without a separate cache.
func (b *boardView) refreshSelection() {
	iss := b.currentIssue()
	if iss == nil {
		b.selected = nil
		b.comments = nil
		b.commentsErr = nil
		return
	}
	if b.selected != nil && b.selected.ID == iss.ID {
		b.selected = iss
		return
	}
	b.selected = iss
	cs, err := b.store.ListComments(iss.ID)
	b.comments = cs
	b.commentsErr = err
	b.overlayScroll = 0
}

func (b *boardView) HasOverlay() bool { return b.overlay || b.picker }

func (b *boardView) Help() string {
	switch {
	case b.picker:
		return "j/k move · space toggle · a all · n none · esc close"
	case b.overlay:
		return "j/k scroll · g/G top/bottom · esc close"
	}
	return "h/l cols · j/k cards · enter open · c columns · H hide col · d detail · r reload · q quit"
}

func (b *boardView) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if b.picker {
		b.updatePicker(key)
		return nil
	}
	if b.overlay {
		switch key.String() {
		case "esc", "enter":
			b.overlay = false
			b.overlayScroll = 0
		case "j", "down":
			b.overlayScroll++
		case "k", "up":
			if b.overlayScroll > 0 {
				b.overlayScroll--
			}
		case "g", "home":
			b.overlayScroll = 0
		case "G", "end":
			b.overlayScroll = 1 << 30 // clamped at render time
		case "pgdown", " ":
			b.overlayScroll += 10
		case "pgup":
			b.overlayScroll -= 10
			if b.overlayScroll < 0 {
				b.overlayScroll = 0
			}
		}
		return nil
	}
	visible := b.visibleStates()
	switch key.String() {
	case "h", "left":
		if b.col > 0 {
			b.col--
		}
	case "l", "right":
		if b.col < len(visible)-1 {
			b.col++
		}
	case "j", "down":
		if len(visible) == 0 {
			break
		}
		st := visible[b.col]
		if b.rows[st] < len(b.columns[st])-1 {
			b.rows[st]++
		}
	case "k", "up":
		if len(visible) == 0 {
			break
		}
		st := visible[b.col]
		if b.rows[st] > 0 {
			b.rows[st]--
		}
	case "g", "home":
		if len(visible) > 0 {
			b.rows[visible[b.col]] = 0
		}
	case "G", "end":
		if len(visible) == 0 {
			break
		}
		st := visible[b.col]
		if n := len(b.columns[st]); n > 0 {
			b.rows[st] = n - 1
		}
	case "r":
		if err := b.reload(); err != nil {
			b.err = err
		}
	case "d":
		b.detailVisible = !b.detailVisible
	case "enter":
		if b.selected != nil {
			b.overlay = true
			b.overlayScroll = 0
		}
	case "c":
		b.openPicker()
	case "H":
		// Quick power-user hide of the focused column. We refuse to hide
		// the last visible column so the board never goes empty by accident.
		if len(visible) <= 1 {
			break
		}
		st := visible[b.col]
		b.hidden[st] = true
		b.persistHidden()
		b.clampCol()
	}
	b.refreshSelection()
	return nil
}

func (b *boardView) openPicker() {
	b.picker = true
	// Park the picker cursor on the focused column so toggling it is a
	// single keystroke after opening.
	b.pickerRow = 0
	visible := b.visibleStates()
	if len(visible) == 0 {
		return
	}
	focusedState := visible[b.col]
	for i, st := range b.states {
		if st == focusedState {
			b.pickerRow = i
			return
		}
	}
}

func (b *boardView) updatePicker(key tea.KeyMsg) {
	switch key.String() {
	case "esc", "q":
		b.picker = false
	case "j", "down":
		if b.pickerRow < len(b.states)-1 {
			b.pickerRow++
		}
	case "k", "up":
		if b.pickerRow > 0 {
			b.pickerRow--
		}
	case "g", "home":
		b.pickerRow = 0
	case "G", "end":
		b.pickerRow = len(b.states) - 1
	case " ", "enter":
		st := b.states[b.pickerRow]
		// Don't allow toggling the last visible column off — keeps the
		// board from rendering as empty.
		if !b.hidden[st] && len(b.visibleStates()) <= 1 {
			break
		}
		b.hidden[st] = !b.hidden[st]
		if !b.hidden[st] {
			delete(b.hidden, st)
		}
		b.persistHidden()
		b.clampCol()
		b.refreshSelection()
	case "a":
		// Show all.
		b.hidden = map[model.State]bool{}
		b.persistHidden()
	case "n":
		// Hide all but the first state — refuses to leave the board empty.
		next := map[model.State]bool{}
		for i, st := range b.states {
			if i == 0 {
				continue
			}
			next[st] = true
		}
		b.hidden = next
		b.persistHidden()
		b.clampCol()
		b.refreshSelection()
	}
}

func (b *boardView) View(width, height int) string {
	if width == 0 || height == 0 {
		return ""
	}
	if b.picker {
		return b.viewPicker(width, height)
	}
	if b.overlay {
		return b.viewOverlay(width, height)
	}

	// Detail pane sized at ~1/3 of body, clamped 8–14 rows; suppressed on
	// short terminals.
	detailHeight := 0
	if b.detailVisible && height >= 20 {
		detailHeight = height / 3
		if detailHeight < 8 {
			detailHeight = 8
		}
		if detailHeight > 14 {
			detailHeight = 14
		}
	}
	colsHeight := height - detailHeight
	if colsHeight < 5 {
		colsHeight = 5
	}

	visible := b.visibleStates()
	if len(visible) == 0 {
		empty := lipgloss.NewStyle().
			Width(width).Height(colsHeight).
			Align(lipgloss.Center, lipgloss.Center).
			Foreground(mutedColor).
			Render("All columns hidden — press c to choose visible columns.")
		return empty
	}

	n := len(visible)
	colWidth := width / n
	if colWidth < 16 {
		colWidth = 16
	}

	cols := make([]string, n)
	for i, st := range visible {
		cols[i] = b.renderColumn(st, i == b.col, colWidth, colsHeight)
	}
	board := lipgloss.JoinHorizontal(lipgloss.Top, cols...)

	parts := []string{board}
	if detailHeight > 0 {
		parts = append(parts, b.renderDetail(width, detailHeight))
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// viewPicker renders the column-visibility modal as a centered card listing
// every state with a checkbox and its current issue count.
func (b *boardView) viewPicker(width, height int) string {
	// Sizing: ~40 cols wide, ~3 rows of chrome plus one row per state.
	innerWidth := 40
	if innerWidth > width-6 {
		innerWidth = max(20, width-6)
	}

	header := boldStyle.Render("Visible columns")
	rowStyle := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1)
	selStyle := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1).
		Background(cardSelectedBG).Foreground(lipgloss.Color("231"))

	var rows []string
	rows = append(rows, header, "")
	for i, st := range b.states {
		mark := "[×]"
		if b.hidden[st] {
			mark = "[ ]"
		}
		count := len(b.columns[st])
		label := fmt.Sprintf("%s  %-13s  %d", mark, stateLabel(st), count)
		if i == b.pickerRow {
			rows = append(rows, selStyle.Render(label))
		} else {
			rows = append(rows, rowStyle.Render(label))
		}
	}
	rows = append(rows, "", mutedStyle.Render("space toggle · a all · n minimal · esc close"))

	card := lipgloss.NewStyle().
		Border(colBorder).BorderForeground(colFocusBorder).
		Padding(1, 2).
		Render(strings.Join(rows, "\n"))

	// Centre the card horizontally and vertically inside the available space.
	return lipgloss.NewStyle().
		Width(width).Height(height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(card)
}

func (b *boardView) renderColumn(st model.State, focused bool, width, height int) string {
	issues := b.columns[st]
	sel := b.rows[st]
	if sel >= len(issues) {
		sel = max(0, len(issues)-1)
	}

	border := colBorderColor
	headerBG := colHeaderUnfocus
	if focused {
		border = colFocusBorder
		headerBG = colHeaderFocus
	}

	innerWidth := width - 2
	if innerWidth < 4 {
		innerWidth = 4
	}

	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("231")).
		Background(headerBG).
		Width(innerWidth).
		Align(lipgloss.Center).
		Render(fmt.Sprintf("%s · %d", stateLabel(st), len(issues)))

	cardStyle := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1)
	selStyle := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1).
		Background(cardSelectedBG).Foreground(lipgloss.Color("231"))

	// Each card occupies exactly cardHeight lines: the first carries the
	// issue key and the start of the title, subsequent lines are wrapped
	// title continuations indented to align under the title text.
	const cardHeight = 2

	var lines []string
	if len(issues) == 0 {
		lines = append(lines, mutedStyle.Width(innerWidth).Padding(1, 1).Render("— empty —"))
	}
	for i, iss := range issues {
		prefixW := len(iss.Key) + 2 // KEY + two-space gutter on line 0 only
		// Line 0 shares its row with the issue key, so it has less title room
		// than wrap-around lines, which start at the left edge.
		firstW := innerWidth - 2 - prefixW
		fullW := innerWidth - 2
		if firstW < 4 {
			firstW = 4
		}
		titleLines := wrapLinesAt(iss.Title, func(line int) int {
			if line == 0 {
				return firstW
			}
			return fullW
		}, cardHeight)

		isSel := i == sel && focused
		styler := cardStyle
		// When the card is selected we deliberately render the key as plain
		// text. Nested lipgloss styles emit their own reset sequence, which
		// punches a black hole in the parent's background even when we set
		// an explicit bg on the inner span. Letting selStyle paint uniformly
		// gives a clean fill at the cost of the key's accent colour.
		keyRender := keyStyle.Render(iss.Key)
		if isSel {
			styler = selStyle
			keyRender = iss.Key
		}
		for j := 0; j < cardHeight; j++ {
			var content string
			switch {
			case j == 0:
				if len(titleLines) > 0 {
					content = keyRender + "  " + titleLines[0]
				} else {
					content = keyRender
				}
			case j < len(titleLines):
				content = titleLines[j]
			default:
				content = ""
			}
			lines = append(lines, styler.Render(content))
		}
	}

	body := strings.Join(lines, "\n")
	content := lipgloss.JoinVertical(lipgloss.Left, header, body)

	return lipgloss.NewStyle().
		Border(colBorder).
		BorderForeground(border).
		Width(innerWidth).
		Height(height - 2).
		Render(content)
}

func (b *boardView) renderDetail(width, height int) string {
	innerWidth := width - 4
	if innerWidth < 10 {
		innerWidth = 10
	}
	innerHeight := height - 2
	if innerHeight < 3 {
		innerHeight = 3
	}

	box := lipgloss.NewStyle().
		Border(colBorder).
		BorderForeground(colFocusBorder).
		Width(width - 2).
		Height(height - 2).
		Padding(0, 1)

	if b.selected == nil {
		return box.Foreground(mutedColor).Render("No issue selected.")
	}

	iss := b.selected
	titleLine := keyStyle.Bold(true).Render(iss.Key) + "  " +
		boldStyle.Render(truncate(iss.Title, innerWidth-len(iss.Key)-2))

	metaParts := []string{"state: " + stateLabel(iss.State)}
	if iss.FeatureSlug != "" {
		metaParts = append(metaParts, "feature: "+iss.FeatureSlug)
	}
	if len(iss.Tags) > 0 {
		metaParts = append(metaParts, "tags: "+strings.Join(iss.Tags, ", "))
	}
	metaParts = append(metaParts, "updated: "+iss.UpdatedAt.Format("2006-01-02 15:04"))
	meta := mutedStyle.Render(truncate(strings.Join(metaParts, " · "), innerWidth))

	var commentLine string
	switch {
	case b.commentsErr != nil:
		commentLine = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).
			Render("comments error: " + b.commentsErr.Error())
	case len(b.comments) == 0:
		commentLine = mutedStyle.Render("0 comments — press enter for full view")
	default:
		c := b.comments[len(b.comments)-1]
		preview := fmt.Sprintf("%d comments · last by %s: %s",
			len(b.comments), c.Author, oneLine(c.Body))
		commentLine = mutedStyle.Render(truncate(preview, innerWidth))
	}

	descRows := innerHeight - 4
	if descRows < 1 {
		descRows = 1
	}
	desc := iss.Description
	if desc == "" {
		desc = mutedStyle.Italic(true).Render("(no description — enter for full view)")
	} else {
		desc = lipgloss.NewStyle().Width(innerWidth).Render(desc)
		desc = clipLines(desc, descRows)
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		titleLine,
		meta,
		"",
		desc,
		commentLine,
	)
	return box.Render(content)
}

// viewOverlay renders a fullscreen card view: title, metadata, the full
// description, and the full comment thread, all within a single scrollable
// region.
func (b *boardView) viewOverlay(width, height int) string {
	innerWidth := width - 4
	if innerWidth < 20 {
		innerWidth = 20
	}

	iss := b.selected
	if iss == nil {
		return lipgloss.NewStyle().
			Border(colBorder).BorderForeground(colFocusBorder).
			Width(width - 2).Height(height - 2).Padding(1, 2).
			Render("No issue selected.")
	}

	titleLine := keyStyle.Bold(true).Render(iss.Key) + "  " +
		boldStyle.Render(iss.Title)

	labelStyle := lipgloss.NewStyle().Foreground(mutedColor).Width(10)
	metaRow := func(label, value string) string {
		return labelStyle.Render(label) + value
	}

	var metaLines []string
	metaLines = append(metaLines, metaRow("state", stateLabel(iss.State)))
	if iss.FeatureSlug != "" {
		metaLines = append(metaLines, metaRow("feature", iss.FeatureSlug))
	}
	if len(iss.Tags) > 0 {
		metaLines = append(metaLines, metaRow("tags", strings.Join(iss.Tags, ", ")))
	}
	metaLines = append(metaLines,
		metaRow("created", iss.CreatedAt.Format("2006-01-02 15:04")),
		metaRow("updated", iss.UpdatedAt.Format("2006-01-02 15:04")),
	)

	descHeader := boldStyle.Render("Description")
	var desc string
	if iss.Description == "" {
		desc = mutedStyle.Italic(true).Render("(none)")
	} else {
		desc = renderMarkdown(iss.Description, innerWidth)
	}

	var commentBlocks []string
	commentHeader := boldStyle.Render(fmt.Sprintf("Comments · %d", len(b.comments)))
	if b.commentsErr != nil {
		commentBlocks = append(commentBlocks, errorStyle.Render(b.commentsErr.Error()))
	} else if len(b.comments) == 0 {
		commentBlocks = append(commentBlocks, mutedStyle.Italic(true).Render("(no comments yet)"))
	} else {
		for _, c := range b.comments {
			head := keyStyle.Render(c.Author) + mutedStyle.Render("  "+c.CreatedAt.Format("2006-01-02 15:04"))
			// Comment bodies render through glamour at slightly indented
			// width so threaded replies stay visually nested without
			// glamour breaking on the leading padding.
			body := renderMarkdown(c.Body, innerWidth-2)
			body = indentLines(body, "  ")
			commentBlocks = append(commentBlocks, head+"\n"+body)
		}
	}

	all := []string{titleLine, ""}
	all = append(all, metaLines...)
	all = append(all, "", descHeader, desc, "", commentHeader)
	all = append(all, commentBlocks...)

	full := strings.Join(all, "\n")
	innerHeight := height - 2 - 2 // borders + padding rows
	if innerHeight < 3 {
		innerHeight = 3
	}

	totalLineCount := totalLines(full)
	maxScroll := max(0, totalLineCount-innerHeight)
	if b.overlayScroll > maxScroll {
		b.overlayScroll = maxScroll
	}

	visible := scrollLines(full, b.overlayScroll, innerHeight)

	// Pad the visible region so the bordered box has a stable shape.
	if missing := innerHeight - totalLines(visible); missing > 0 {
		visible += strings.Repeat("\n", missing)
	}

	scrollHint := ""
	if maxScroll > 0 {
		scrollHint = mutedStyle.Render(fmt.Sprintf(" %d/%d ", b.overlayScroll, maxScroll))
	}

	box := lipgloss.NewStyle().
		Border(colBorder).
		BorderForeground(colFocusBorder).
		Width(width - 2).
		Padding(1, 2).
		Render(visible)

	if scrollHint != "" {
		// Anchor the scroll indicator to the bottom-right.
		box = lipgloss.JoinVertical(lipgloss.Left, box,
			lipgloss.NewStyle().Width(width-2).Align(lipgloss.Right).Render(scrollHint),
		)
	}
	return box
}

func stateLabel(st model.State) string {
	switch st {
	case model.StateBacklog:
		return "Backlog"
	case model.StateTodo:
		return "Todo"
	case model.StateInProgress:
		return "In Progress"
	case model.StateInReview:
		return "In Review"
	case model.StateDone:
		return "Done"
	case model.StateCancelled:
		return "Cancelled"
	case model.StateDuplicate:
		return "Duplicate"
	}
	return string(st)
}
