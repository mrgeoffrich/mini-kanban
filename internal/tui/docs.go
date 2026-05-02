package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"mini-kanban/internal/model"
	"mini-kanban/internal/store"
)

// docsView is a two-pane document library: filenames on the left, content
// preview on the right. Pressing enter opens a fullscreen overlay so users
// can read a long doc without leaving the TUI.
type docsView struct {
	store *store.Store
	repo  *model.Repo

	docs    []*model.Document
	row     int
	loaded  *model.Document // selected doc with content (lazy loaded)
	loadErr error

	overlay       bool
	overlayScroll int

	err error
}

func newDocsView(s *store.Store, repo *model.Repo) *docsView {
	d := &docsView{store: s, repo: repo}
	d.reload()
	d.refreshSelection()
	return d
}

func (d *docsView) reload() {
	docs, err := d.store.ListDocuments(store.DocumentFilter{RepoID: d.repo.ID})
	if err != nil {
		d.err = err
		return
	}
	d.err = nil
	d.docs = docs
	if d.row >= len(docs) {
		d.row = max(0, len(docs)-1)
	}
}

func (d *docsView) currentDoc() *model.Document {
	if len(d.docs) == 0 {
		return nil
	}
	if d.row >= len(d.docs) {
		d.row = len(d.docs) - 1
	}
	if d.row < 0 {
		d.row = 0
	}
	return d.docs[d.row]
}

func (d *docsView) refreshSelection() {
	cur := d.currentDoc()
	if cur == nil {
		d.loaded = nil
		d.loadErr = nil
		return
	}
	if d.loaded != nil && d.loaded.ID == cur.ID {
		return
	}
	doc, err := d.store.GetDocumentByID(cur.ID, true)
	d.loaded = doc
	d.loadErr = err
	d.overlayScroll = 0
}

func (d *docsView) HasOverlay() bool { return d.overlay }

func (d *docsView) Help() string {
	if d.overlay {
		return "j/k scroll · g/G top/bottom · esc close"
	}
	return "j/k docs · enter open · r reload · q quit"
}

func (d *docsView) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if d.overlay {
		switch key.String() {
		case "esc", "enter":
			d.overlay = false
			d.overlayScroll = 0
		case "j", "down":
			d.overlayScroll++
		case "k", "up":
			if d.overlayScroll > 0 {
				d.overlayScroll--
			}
		case "g", "home":
			d.overlayScroll = 0
		case "G", "end":
			d.overlayScroll = 1 << 30
		case "pgdown", " ":
			d.overlayScroll += 10
		case "pgup":
			d.overlayScroll -= 10
			if d.overlayScroll < 0 {
				d.overlayScroll = 0
			}
		}
		return nil
	}
	switch key.String() {
	case "j", "down":
		if d.row < len(d.docs)-1 {
			d.row++
		}
	case "k", "up":
		if d.row > 0 {
			d.row--
		}
	case "g", "home":
		d.row = 0
	case "G", "end":
		if len(d.docs) > 0 {
			d.row = len(d.docs) - 1
		}
	case "r":
		d.reload()
	case "enter":
		if d.loaded != nil {
			d.overlay = true
			d.overlayScroll = 0
		}
	}
	d.refreshSelection()
	return nil
}

func (d *docsView) View(width, height int) string {
	if width == 0 || height == 0 {
		return ""
	}
	if d.overlay {
		return d.viewOverlay(width, height)
	}

	// Left list pane gets ~32 cols (or 1/3, whichever is larger), right pane
	// takes the rest. Both share the available height.
	listW := width / 3
	if listW < 28 {
		listW = 28
	}
	if listW > 48 {
		listW = 48
	}
	if listW > width-20 {
		listW = max(20, width-20)
	}
	previewW := width - listW

	return lipgloss.JoinHorizontal(lipgloss.Top,
		d.renderList(listW, height),
		d.renderPreview(previewW, height),
	)
}

func (d *docsView) renderList(width, height int) string {
	innerWidth := width - 2
	if innerWidth < 6 {
		innerWidth = 6
	}

	header := lipgloss.NewStyle().
		Bold(true).Foreground(lipgloss.Color("231")).Background(colHeaderFocus).
		Width(innerWidth).Align(lipgloss.Center).
		Render(fmt.Sprintf("Documents · %d", len(d.docs)))

	rowStyle := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1)
	selStyle := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1).
		Background(cardSelectedBG).Foreground(lipgloss.Color("231"))

	var lines []string
	if len(d.docs) == 0 {
		lines = append(lines, mutedStyle.Padding(1, 1).Render("— no documents —"))
	}
	for i, doc := range d.docs {
		typeTag := mutedStyle.Render(shortDocType(doc.Type))
		line := truncate(doc.Filename, innerWidth-len(shortDocType(doc.Type))-3) + "  " + typeTag
		if i == d.row {
			lines = append(lines, selStyle.Render(line))
		} else {
			lines = append(lines, rowStyle.Render(line))
		}
	}

	body := strings.Join(lines, "\n")
	content := lipgloss.JoinVertical(lipgloss.Left, header, body)

	return lipgloss.NewStyle().
		Border(colBorder).BorderForeground(colFocusBorder).
		Width(innerWidth).Height(height - 2).
		Render(content)
}

func (d *docsView) renderPreview(width, height int) string {
	innerWidth := width - 4
	if innerWidth < 10 {
		innerWidth = 10
	}
	innerHeight := height - 2
	if innerHeight < 3 {
		innerHeight = 3
	}

	box := lipgloss.NewStyle().
		Border(colBorder).BorderForeground(colBorderColor).
		Width(width - 2).Height(height - 2).Padding(0, 1)

	if d.err != nil {
		return box.Render(errorStyle.Render(d.err.Error()))
	}
	if d.loadErr != nil {
		return box.Render(errorStyle.Render(d.loadErr.Error()))
	}
	if d.loaded == nil {
		return box.Foreground(mutedColor).Render("No document selected.")
	}

	doc := d.loaded
	title := boldStyle.Render(truncate(doc.Filename, innerWidth))
	meta := mutedStyle.Render(truncate(fmt.Sprintf("%s · %s · updated %s",
		doc.Type, humanBytes(doc.SizeBytes), doc.UpdatedAt.Format("2006-01-02 15:04")),
		innerWidth))

	contentRows := innerHeight - 4
	if contentRows < 1 {
		contentRows = 1
	}
	body := lipgloss.NewStyle().Width(innerWidth).Render(doc.Content)
	body = clipLines(body, contentRows)

	hint := mutedStyle.Italic(true).Render("(enter for full view)")

	content := lipgloss.JoinVertical(lipgloss.Left, title, meta, "", body, hint)
	return box.Render(content)
}

func (d *docsView) viewOverlay(width, height int) string {
	innerWidth := width - 4
	if innerWidth < 20 {
		innerWidth = 20
	}

	if d.loaded == nil {
		return lipgloss.NewStyle().
			Border(colBorder).BorderForeground(colFocusBorder).
			Width(width - 2).Height(height - 2).Padding(1, 2).
			Render("No document selected.")
	}

	doc := d.loaded
	title := boldStyle.Render(doc.Filename)
	meta := mutedStyle.Render(fmt.Sprintf("%s · %s · created %s · updated %s",
		doc.Type, humanBytes(doc.SizeBytes),
		doc.CreatedAt.Format("2006-01-02 15:04"),
		doc.UpdatedAt.Format("2006-01-02 15:04"),
	))
	body := lipgloss.NewStyle().Width(innerWidth).Render(doc.Content)

	full := strings.Join([]string{title, meta, "", body}, "\n")
	innerHeight := height - 2 - 2
	if innerHeight < 3 {
		innerHeight = 3
	}

	totalLineCount := totalLines(full)
	maxScroll := max(0, totalLineCount-innerHeight)
	if d.overlayScroll > maxScroll {
		d.overlayScroll = maxScroll
	}

	visible := scrollLines(full, d.overlayScroll, innerHeight)
	if missing := innerHeight - totalLines(visible); missing > 0 {
		visible += strings.Repeat("\n", missing)
	}

	box := lipgloss.NewStyle().
		Border(colBorder).BorderForeground(colFocusBorder).
		Width(width - 2).Padding(1, 2).
		Render(visible)
	if maxScroll > 0 {
		hint := mutedStyle.Render(fmt.Sprintf(" %d/%d ", d.overlayScroll, maxScroll))
		box = lipgloss.JoinVertical(lipgloss.Left, box,
			lipgloss.NewStyle().Width(width-2).Align(lipgloss.Right).Render(hint),
		)
	}
	return box
}

func shortDocType(t model.DocumentType) string {
	switch t {
	case model.DocTypeUserDocs:
		return "user"
	case model.DocTypeProjectInPlanning:
		return "planning"
	case model.DocTypeProjectInProgress:
		return "in-prog"
	case model.DocTypeProjectComplete:
		return "complete"
	case model.DocTypeVendorDocs:
		return "vendor"
	case model.DocTypeArchitecture:
		return "arch"
	case model.DocTypeDesigns:
		return "design"
	case model.DocTypeTestingPlans:
		return "test"
	}
	return string(t)
}

func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	}
}
