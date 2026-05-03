package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/mini-kanban/internal/model"
	"github.com/mrgeoffrich/mini-kanban/internal/store"
)

// boardRefreshInterval is how often the board reloads issues from the
// store while the TUI is open. Short enough to feel live, long enough
// not to thrash SQLite.
const boardRefreshInterval = 30 * time.Second

// overlayPane identifies which sub-pane of the fullscreen card overlay
// currently has the focus, so j/k and enter route to the right place
// and the matching header gets the accent treatment.
type overlayPane int

const (
	paneDescription overlayPane = iota
	paneComments
	paneAttachments
)

// boardRefreshMsg is delivered by tea.Tick to trigger a reload.
type boardRefreshMsg time.Time

func boardRefreshTick() tea.Cmd {
	return tea.Tick(boardRefreshInterval, func(t time.Time) tea.Msg {
		return boardRefreshMsg(t)
	})
}

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
	scroll        map[model.State]int // top visible card index per column
	detailVisible bool

	selected    *model.Issue
	comments    []*model.Comment
	commentsErr error
	docLinks    []*model.DocumentLink
	prs         []*model.PullRequest
	attachErr   error

	overlay       bool
	overlayFocus  overlayPane // which sub-pane has focus inside the card overlay
	overlayScroll int         // scroll offset for the description (top pane)
	commentsRow   int         // scroll offset for the comments list
	attachRow     int         // cursor index in the attachments list

	picker    bool
	pickerRow int

	lastRefresh time.Time

	mdCache map[int]mdCacheEntry // see docsView for shape

	err error
}

func (b *boardView) cachedMD(id int64, src string, width int) string {
	if b.mdCache == nil {
		b.mdCache = map[int]mdCacheEntry{}
	}
	if e, ok := b.mdCache[width]; ok && e.id == id {
		return e.out
	}
	out := renderMarkdown(src, width)
	b.mdCache[width] = mdCacheEntry{id: id, out: out}
	return out
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
		scroll:        map[model.State]int{},
		detailVisible: true,
	}
	if err := b.reload(); err != nil {
		return nil, err
	}
	b.lastRefresh = time.Now()
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
	// Issue descriptions may have changed under us — start the cache
	// fresh so stale renders aren't served until a new render replaces
	// them.
	b.mdCache = nil
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

// refreshSelection re-fetches comments and attachments only when the
// selected issue changes — keeps navigation snappy without a separate
// cache.
func (b *boardView) refreshSelection() {
	iss := b.currentIssue()
	if iss == nil {
		b.selected = nil
		b.comments = nil
		b.commentsErr = nil
		b.docLinks = nil
		b.prs = nil
		b.attachErr = nil
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
	docs, derr := b.store.ListDocumentsLinkedToIssue(iss.ID)
	b.docLinks = docs
	prs, perr := b.store.ListPRs(iss.ID)
	b.prs = prs
	switch {
	case derr != nil:
		b.attachErr = derr
	case perr != nil:
		b.attachErr = perr
	default:
		b.attachErr = nil
	}
	b.overlayScroll = 0
}

func (b *boardView) Init() tea.Cmd { return boardRefreshTick() }

func (b *boardView) Status() string {
	if b.lastRefresh.IsZero() {
		return ""
	}
	return "↻ " + b.lastRefresh.Format("15:04:05")
}

func (b *boardView) HasOverlay() bool { return b.overlay || b.picker }

func (b *boardView) Help() string {
	switch {
	case b.picker:
		return "j/k move · space toggle · a all · n none · esc close"
	case b.overlay:
		switch b.overlayFocus {
		case paneComments:
			return "tab next pane · j/k scroll comments · esc close"
		case paneAttachments:
			return "tab next pane · j/k select · enter open · esc close"
		}
		return "tab next pane · j/k scroll · g/G top/bottom · esc close"
	}
	return "h/l cols · j/k cards · enter open · c columns · H hide col · d detail · r reload · q quit"
}

func (b *boardView) Update(msg tea.Msg) tea.Cmd {
	if t, ok := msg.(boardRefreshMsg); ok {
		// Periodic background reload. We pull fresh issues, refresh the
		// selected card's comments, and schedule the next tick. Errors
		// surface in the footer rather than crashing the loop.
		if err := b.reload(); err != nil {
			b.err = err
		} else {
			b.err = nil
		}
		b.lastRefresh = time.Time(t)
		b.clampCol()
		b.refreshSelection()
		return boardRefreshTick()
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if b.picker {
		b.updatePicker(key)
		return nil
	}
	if b.overlay {
		return b.updateOverlay(key)
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
		} else {
			b.err = nil
		}
		b.lastRefresh = time.Now()
	case "d":
		b.detailVisible = !b.detailVisible
	case "enter":
		if b.selected != nil {
			b.overlay = true
			b.overlayFocus = paneDescription
			b.overlayScroll = 0
			b.commentsRow = 0
			b.attachRow = 0
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

// updateOverlay handles input while the fullscreen card overlay is up.
// Tab cycles focus through description / comments / attachments; j/k/g/G
// route to the focused pane only.
func (b *boardView) updateOverlay(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc":
		b.overlay = false
		return nil
	case "tab":
		b.overlayFocus = (b.overlayFocus + 1) % 3
		return nil
	case "shift+tab":
		b.overlayFocus = (b.overlayFocus + 2) % 3
		return nil
	}

	switch b.overlayFocus {
	case paneDescription:
		switch key.String() {
		case "enter":
			b.overlay = false
		case "j", "down":
			b.overlayScroll++
		case "k", "up":
			if b.overlayScroll > 0 {
				b.overlayScroll--
			}
		case "g", "home":
			b.overlayScroll = 0
		case "G", "end":
			b.overlayScroll = 1 << 30
		case "pgdown", " ":
			b.overlayScroll += 10
		case "pgup":
			b.overlayScroll -= 10
			if b.overlayScroll < 0 {
				b.overlayScroll = 0
			}
		}
	case paneComments:
		switch key.String() {
		case "enter":
			b.overlay = false
		case "j", "down":
			b.commentsRow++
		case "k", "up":
			if b.commentsRow > 0 {
				b.commentsRow--
			}
		case "g", "home":
			b.commentsRow = 0
		case "G", "end":
			b.commentsRow = 1 << 30
		}
	case paneAttachments:
		total := len(b.docLinks) + len(b.prs)
		switch key.String() {
		case "enter":
			// Enter on an attachment is more useful than "close
			// overlay" — the user can still close via esc.
			return b.openSelectedAttachment()
		case "j", "down":
			if b.attachRow < total-1 {
				b.attachRow++
			}
		case "k", "up":
			if b.attachRow > 0 {
				b.attachRow--
			}
		case "g", "home":
			b.attachRow = 0
		case "G", "end":
			if total > 0 {
				b.attachRow = total - 1
			}
		}
	}
	return nil
}

// openSelectedAttachment fires an openDocMsg for a selected document so
// the shell can switch tabs and open it. PRs aren't actionable yet —
// pressing enter on one is a no-op until we wire a "copy URL" or
// browser-launch action.
func (b *boardView) openSelectedAttachment() tea.Cmd {
	docs := len(b.docLinks)
	if b.attachRow < 0 || b.attachRow >= docs {
		return nil
	}
	filename := b.docLinks[b.attachRow].DocumentFilename
	return func() tea.Msg {
		return openDocMsg{filename: filename}
	}
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

	// Each card occupies exactly cardHeight lines: the first carries the
	// issue key and the start of the title, subsequent lines are wrapped
	// title continuations indented to align under the title text.
	const cardHeight = 2
	const headerRows = 1

	// Compute how many cards fit in the body, then nudge the per-column
	// scroll so the cursor stays in view.
	bodyRows := height - 2 - headerRows // -2 for top/bottom borders
	if bodyRows < cardHeight {
		bodyRows = cardHeight
	}
	cardsPerCol := bodyRows / cardHeight
	if cardsPerCol < 1 {
		cardsPerCol = 1
	}
	scroll := b.scroll[st]
	if scroll > sel {
		scroll = sel
	}
	if sel >= scroll+cardsPerCol {
		scroll = sel - cardsPerCol + 1
	}
	maxScroll := len(issues) - cardsPerCol
	if maxScroll < 0 {
		maxScroll = 0
	}
	if scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}
	b.scroll[st] = scroll

	end := scroll + cardsPerCol
	if end > len(issues) {
		end = len(issues)
	}
	moreAbove := scroll > 0
	moreBelow := end < len(issues)

	indicator := ""
	switch {
	case moreAbove && moreBelow:
		indicator = " ↕"
	case moreAbove:
		indicator = " ↑"
	case moreBelow:
		indicator = " ↓"
	}

	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("231")).
		Background(headerBG).
		Width(innerWidth).
		Align(lipgloss.Center).
		Render(fmt.Sprintf("%s · %d%s", stateLabel(st), len(issues), indicator))

	// Cards run flush against the column border on purpose: when columns
	// get narrow, every column counts. The spacious vs packed layout below
	// uses the full inner width on continuation lines.
	cardStyle := lipgloss.NewStyle().Width(innerWidth)
	selStyle := lipgloss.NewStyle().Width(innerWidth).
		Background(cardSelectedBG).Foreground(lipgloss.Color("231"))

	var lines []string
	if len(issues) == 0 {
		lines = append(lines, mutedStyle.Width(innerWidth).Padding(1, 1).Render("— empty —"))
	}
	for i := scroll; i < end; i++ {
		iss := issues[i]
		bracketed := "[" + iss.Key + "]"
		// No more cardStyle padding — content uses the full inner width.
		fullW := innerWidth

		isSel := i == sel && focused
		styler := cardStyle
		// When the card is selected we deliberately render the key as plain
		// text. Nested lipgloss styles emit their own reset sequence, which
		// punches a black hole in the parent's background even when we set
		// an explicit bg on the inner span. Letting selStyle paint uniformly
		// gives a clean fill at the cost of the key's accent colour.
		keyRender := keyStyle.Render(bracketed)
		if isSel {
			styler = selStyle
			keyRender = bracketed
		}

		// If the title fits on a single line at the full inner width, use
		// the spacious layout: [KEY] alone on line 0, title alone on line
		// 1. This avoids the line-0 prefix gutter eating into the title's
		// budget for the common short-title case.
		if titleRunes := []rune(iss.Title); len(titleRunes) <= fullW {
			lines = append(lines, styler.Render(keyRender))
			lines = append(lines, styler.Render(iss.Title))
			continue
		}

		// Title needs both lines. Pack [KEY] + the start of the title on
		// line 0, then the continuation on line 1.
		prefixW := len(bracketed) + 2 // [KEY] + two-space gutter
		firstW := innerWidth - prefixW
		if firstW < 4 {
			firstW = 4
		}
		titleLines := wrapLinesAt(iss.Title, func(line int) int {
			if line == 0 {
				return firstW
			}
			return fullW
		}, cardHeight)

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
		BorderForeground(colBorderColor).
		Width(width-2).
		Height(height-2).
		Padding(0, 1)

	if b.selected == nil {
		return box.Foreground(mutedColor).Render("No issue selected.")
	}

	iss := b.selected
	bracketedKey := "[" + iss.Key + "]"
	previewTag := mutedStyle.Italic(true).Render("preview")
	previewW := lipgloss.Width(previewTag)
	// Reserve room on the right for the "preview" label plus a 2-col gap.
	titleSpace := innerWidth - len(bracketedKey) - 2 - previewW - 2
	if titleSpace < 4 {
		titleSpace = 4
	}
	titleContent := keyStyle.Bold(true).Render(bracketedKey) + "  " +
		boldStyle.Render(truncate(iss.Title, titleSpace))
	gap := innerWidth - lipgloss.Width(titleContent) - previewW
	if gap < 1 {
		gap = 1
	}
	titleLine := lipgloss.JoinHorizontal(lipgloss.Top,
		titleContent,
		lipgloss.NewStyle().Width(gap).Render(""),
		previewTag,
	)

	metaParts := []string{"state: " + stateLabel(iss.State)}
	if iss.FeatureSlug != "" {
		metaParts = append(metaParts, "feature: ["+iss.FeatureSlug+"]")
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
	var desc string
	if iss.Description == "" {
		desc = mutedStyle.Italic(true).Render("(no description — enter for full view)")
	} else {
		desc = b.cachedMD(iss.ID, iss.Description, innerWidth)
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

// viewOverlay renders the fullscreen card view as a vertical split:
//   - top ~70% — title, metadata, scrollable description (with scrollbar)
//   - bottom ~30% — two side-by-side panes: comments (left) | attachments
//     (right), each clipped to fit with a "+N more" hint when overflowing.
func (b *boardView) viewOverlay(width, height int) string {
	// Box uses Padding(1, 2): 2 cols of horizontal padding on each side
	// inside Width(width-2). True content area = (width-2) - 4 = width-6.
	innerWidth := width - 6
	if innerWidth < 20 {
		innerWidth = 20
	}

	iss := b.selected
	if iss == nil {
		return lipgloss.NewStyle().
			Border(colBorder).BorderForeground(colFocusBorder).
			Width(width-2).Height(height-2).Padding(1, 2).
			Render("No issue selected.")
	}

	innerHeight := height - 2 - 2 // borders (2) + vertical padding (2)
	if innerHeight < 8 {
		innerHeight = 8
	}

	// Vertical split. Bottom takes ~30% and is bounded so it stays usable
	// on tall terminals and doesn't disappear on short ones. The 1-row
	// divider sits between the two halves.
	bottomH := innerHeight * 3 / 10
	if bottomH < 6 {
		bottomH = 6
	}
	if bottomH > innerHeight-6 {
		bottomH = max(3, innerHeight-6)
	}
	topH := innerHeight - bottomH - 1 // -1 for divider
	if topH < 3 {
		topH = 3
	}

	top := b.renderOverlayTop(iss, innerWidth, topH)
	divider := mutedStyle.Render(strings.Repeat("─", innerWidth))
	bottom := b.renderOverlayBottom(innerWidth, bottomH)

	body := lipgloss.JoinVertical(lipgloss.Left, top, divider, bottom)

	return lipgloss.NewStyle().
		Border(colBorder).
		BorderForeground(colFocusBorder).
		Width(width-2).
		Padding(1, 2).
		Render(body)
}

// renderOverlayTop is the scrollable upper region: title, metadata, and the
// full markdown description. The right column carries a scrollbar so users
// can see how much description they have left.
func (b *boardView) renderOverlayTop(iss *model.Issue, width, height int) string {
	contentWidth := width - 1 // 1 col reserved for scrollbar
	if contentWidth < 10 {
		contentWidth = 10
	}

	titleLine := keyStyle.Bold(true).Render("["+iss.Key+"]") + "  " +
		boldStyle.Render(iss.Title)

	labelStyle := lipgloss.NewStyle().Foreground(mutedColor).Width(10)
	metaRow := func(label, value string) string {
		return labelStyle.Render(label) + value
	}

	var metaLines []string
	metaLines = append(metaLines, metaRow("state", stateLabel(iss.State)))
	if iss.FeatureSlug != "" {
		metaLines = append(metaLines, metaRow("feature", "["+iss.FeatureSlug+"]"))
	}
	if len(iss.Tags) > 0 {
		metaLines = append(metaLines, metaRow("tags", strings.Join(iss.Tags, ", ")))
	}
	metaLines = append(metaLines,
		metaRow("created", iss.CreatedAt.Format("2006-01-02 15:04")),
		metaRow("updated", iss.UpdatedAt.Format("2006-01-02 15:04")),
	)

	descHeader := paneHeading("Description", b.overlayFocus == paneDescription)
	var desc string
	if iss.Description == "" {
		desc = mutedStyle.Italic(true).Render("(none)")
	} else {
		desc = b.cachedMD(iss.ID, iss.Description, contentWidth)
	}

	all := []string{titleLine, ""}
	all = append(all, metaLines...)
	all = append(all, "", descHeader, desc)

	full := strings.Join(all, "\n")
	totalLineCount := totalLines(full)
	maxScroll := max(0, totalLineCount-height)
	if b.overlayScroll > maxScroll {
		b.overlayScroll = maxScroll
	}

	visible := scrollLines(full, b.overlayScroll, height)
	if missing := height - totalLines(visible); missing > 0 {
		visible += strings.Repeat("\n", missing)
	}
	visible = lipgloss.NewStyle().Width(contentWidth).Render(visible)

	scrollbar := renderVerticalScrollbar(height, totalLineCount, b.overlayScroll)
	return lipgloss.JoinHorizontal(lipgloss.Top, visible, scrollbar)
}

// paneHeading returns a section header rendered with an accent indicator
// when the pane is focused, plain bold when it isn't. The leading "▸"
// gives the eye a clean target without spending a colour.
func paneHeading(label string, focused bool) string {
	if focused {
		return lipgloss.NewStyle().Bold(true).Foreground(colFocusBorder).
			Render("▸ " + label)
	}
	return boldStyle.Render("  " + label)
}

// renderOverlayBottom is the side-by-side comments / attachments panes.
// Both clip to the available height — there's no per-pane scroll yet.
func (b *boardView) renderOverlayBottom(width, height int) string {
	leftW := width / 2
	rightW := width - leftW - 1 // -1 for vertical separator column

	left := b.renderCommentsPane(leftW, height)
	right := b.renderAttachmentsPane(rightW, height)
	sepCol := strings.TrimRight(strings.Repeat(mutedStyle.Render("│")+"\n", height), "\n")

	return lipgloss.JoinHorizontal(lipgloss.Top, left, sepCol, right)
}

func (b *boardView) renderCommentsPane(width, height int) string {
	header := paneHeading(fmt.Sprintf("Comments · %d", len(b.comments)),
		b.overlayFocus == paneComments)
	var body []string
	switch {
	case b.commentsErr != nil:
		body = append(body, errorStyle.Render(b.commentsErr.Error()))
	case len(b.comments) == 0:
		body = append(body, mutedStyle.Italic(true).Render("(no comments yet)"))
	default:
		for _, c := range b.comments {
			head := keyStyle.Render(c.Author) +
				mutedStyle.Render("  "+c.CreatedAt.Format("2006-01-02 15:04"))
			line := lipgloss.NewStyle().Width(width).Render(oneLine(c.Body))
			body = append(body, head, line, "")
		}
	}
	return paneScrollFrame(header, body, width, height, b.commentsRow, false, -1)
}

func (b *boardView) renderAttachmentsPane(width, height int) string {
	count := len(b.docLinks) + len(b.prs)
	header := paneHeading(fmt.Sprintf("Attachments · %d", count),
		b.overlayFocus == paneAttachments)

	var body []string
	switch {
	case b.attachErr != nil:
		body = append(body, errorStyle.Render(b.attachErr.Error()))
	case count == 0:
		body = append(body, mutedStyle.Italic(true).Render("(none)"))
	default:
		for _, l := range b.docLinks {
			line := mutedStyle.Render("📄 ") + truncate(l.DocumentFilename, width-3)
			if l.Description != "" {
				line += mutedStyle.Render("  — " + truncate(l.Description, width/2))
			}
			body = append(body, line)
		}
		for _, pr := range b.prs {
			body = append(body, mutedStyle.Render("🔀 ")+truncate(pr.URL, width-3))
		}
	}

	// Cursor only matters when the attachments pane is focused — otherwise
	// drawing the highlight bar would mislead the user about what j/k
	// would scroll.
	cursor := -1
	if b.overlayFocus == paneAttachments && count > 0 {
		if b.attachRow >= count {
			b.attachRow = count - 1
		}
		cursor = b.attachRow
	}
	return paneScrollFrame(header, body, width, height, 0, true, cursor)
}

// paneScrollFrame composes a focused-pane: a sticky header row plus a
// height-1 body window that the caller can scroll (offset) and/or
// highlight a single row in (cursorIdx). When highlightCursor is false
// the row offset is treated as a pure scroll position.
func paneScrollFrame(header string, body []string, width, height, offset int, highlightCursor bool, cursorIdx int) string {
	rowsAvailable := height - 1
	if rowsAvailable < 1 {
		rowsAvailable = 1
	}

	// Adjust scroll/cursor so the cursor (if any) stays in view.
	if highlightCursor && cursorIdx >= 0 {
		if cursorIdx < offset {
			offset = cursorIdx
		}
		if cursorIdx >= offset+rowsAvailable {
			offset = cursorIdx - rowsAvailable + 1
		}
	}
	maxOffset := len(body) - rowsAvailable
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	if offset < 0 {
		offset = 0
	}

	end := offset + rowsAvailable
	if end > len(body) {
		end = len(body)
	}
	visible := append([]string(nil), body[offset:end]...)

	if highlightCursor && cursorIdx >= offset && cursorIdx < end {
		i := cursorIdx - offset
		visible[i] = lipgloss.NewStyle().Width(width).
			Background(cardSelectedBG).Foreground(lipgloss.Color("231")).
			Render(visible[i])
	}

	for len(visible) < rowsAvailable {
		visible = append(visible, "")
	}

	all := append([]string{header}, visible...)
	return lipgloss.NewStyle().Width(width).Render(strings.Join(all, "\n"))
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
