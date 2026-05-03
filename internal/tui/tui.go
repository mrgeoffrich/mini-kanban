// Package tui implements the full-screen terminal UI for `mk tui`.
//
// The shell defined here owns the alt-screen Bubble Tea program, the tab
// strip, and the top-level key bindings (quit, switch tab). Each tab is a
// `view`: a self-contained widget that handles its own input, state, and
// rendering. Views can declare an active overlay (e.g. fullscreen card or
// document viewer); when one is up, the shell routes all keys to the view
// and stops intercepting tab/digit/quit so the overlay can own the screen.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"mini-kanban/internal/model"
	"mini-kanban/internal/store"
)

// view is the contract every tab implements. Shell mutates state by handing
// messages to Update; rendering is a pure function of (width, height).
//
// Init returns an optional one-shot Cmd run at program startup — used by
// the board to kick off its 30-second refresh tick. Status returns an
// optional right-aligned chip rendered in the footer (e.g. "↻ 14:23:05"
// for the board's last refresh time); empty strings are skipped.
type view interface {
	Init() tea.Cmd
	Update(msg tea.Msg) tea.Cmd
	View(width, height int) string
	Help() string
	HasOverlay() bool
	Status() string
}

type tab struct {
	name string
	v    view
}

// openDocMsg is emitted by the board's attachments pane when the user
// presses enter on a linked document. The shell intercepts it and hands
// the filename to the Documents tab, switching focus.
type openDocMsg struct {
	filename string
}

// Run boots the Bubble Tea program in alt-screen mode and blocks until
// quit. The store is owned by the caller.
func Run(s *store.Store, repo *model.Repo) error {
	board, err := newBoardView(s, repo)
	if err != nil {
		return err
	}
	m := &Model{
		repo: repo,
		tabs: []tab{
			{"Board", board},
			{"Features", newFeaturesView(s, repo)},
			{"Documents", newDocsView(s, repo)},
			{"History", newHistoryView(s, repo)},
		},
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}

type Model struct {
	repo   *model.Repo
	tabs   []tab
	active int
	width  int
	height int
}

func (m *Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range m.tabs {
		if c := t.v.Init(); c != nil {
			cmds = append(cmds, c)
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case openDocMsg:
		// Cross-tab: hand the filename to the Documents tab and focus it.
		for i, t := range m.tabs {
			if t.name != "Documents" {
				continue
			}
			if dv, ok := t.v.(*docsView); ok {
				dv.selectByFilename(msg.filename)
			}
			m.active = i
			return m, nil
		}
		return m, nil
	case tea.KeyMsg:
		// ctrl+c always quits, even past an overlay.
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		// When no overlay is up, shell-level keys win. Otherwise the
		// active view handles everything (so esc closes the overlay
		// instead of quitting the program, etc).
		if !m.tabs[m.active].v.HasOverlay() {
			s := msg.String()
			switch s {
			case "q", "esc":
				return m, tea.Quit
			case "tab":
				m.active = (m.active + 1) % len(m.tabs)
				return m, nil
			case "shift+tab":
				m.active = (m.active - 1 + len(m.tabs)) % len(m.tabs)
				return m, nil
			}
			// Digit shortcuts: "1" through "9" jump to that tab if it
			// exists. Dynamic so adding a 5th view doesn't need a code
			// change here.
			if len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
				idx := int(s[0] - '1')
				if idx < len(m.tabs) {
					m.active = idx
					return m, nil
				}
			}
		}
		return m, m.tabs[m.active].v.Update(msg)
	}
	// Non-key, non-windowsize messages (ticks, custom commands) get
	// broadcast to every view so a tab can receive replies to its own
	// commands even while another tab is active.
	var cmds []tea.Cmd
	for _, t := range m.tabs {
		if c := t.v.Update(msg); c != nil {
			cmds = append(cmds, c)
		}
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	header := m.renderHeader()
	footer := m.renderFooter()
	bodyHeight := m.height - lipgloss.Height(header) - lipgloss.Height(footer)
	if bodyHeight < 5 {
		bodyHeight = 5
	}
	body := m.tabs[m.active].v.View(m.width, bodyHeight)
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

// renderFooter lays out left-aligned help and a right-aligned status chip
// (e.g. the board's auto-refresh timestamp). Both come from the active view.
func (m *Model) renderFooter() string {
	helpText := footerStyle.Render(" " + m.tabs[m.active].v.Help() + " ")
	status := m.tabs[m.active].v.Status()
	if status == "" {
		return helpText
	}
	statusText := footerStyle.Render(status + " ")
	gap := m.width - lipgloss.Width(helpText) - lipgloss.Width(statusText)
	if gap < 1 {
		gap = 1
	}
	spacer := lipgloss.NewStyle().Width(gap).Render("")
	return lipgloss.JoinHorizontal(lipgloss.Top, helpText, spacer, statusText)
}

func (m *Model) renderHeader() string {
	displayName := m.repo.Name
	if owner := parseRepoOwner(m.repo.RemoteURL); owner != "" {
		displayName = owner + "/" + m.repo.Name
	}
	repoTag := titleStyle.Render(fmt.Sprintf("%s — %s %s", m.repo.Prefix, repoGlyph, displayName))

	var tabParts []string
	for i, t := range m.tabs {
		label := fmt.Sprintf("%d %s", i+1, t.name)
		if i == m.active {
			tabParts = append(tabParts, tabActive.Render(label))
		} else {
			tabParts = append(tabParts, tabInactive.Render(label))
		}
	}
	tabs := lipgloss.JoinHorizontal(lipgloss.Top, tabParts...)

	gap := m.width - lipgloss.Width(repoTag) - lipgloss.Width(tabs)
	if gap < 1 {
		gap = 1
	}
	spacer := lipgloss.NewStyle().Width(gap).Render("")
	return lipgloss.JoinHorizontal(lipgloss.Top, repoTag, spacer, tabs)
}

// parseRepoOwner extracts the owner/org segment from a git remote URL. It
// understands both SSH ("git@host:owner/repo.git") and HTTPS-style remotes
// ("https://host/owner/repo.git"), and preserves multi-level paths so
// GitLab subgroups ("group/subgroup/repo") still surface correctly. Returns
// "" when the URL is empty or unparseable, and the caller falls back to
// just the repo name.
func parseRepoOwner(remoteURL string) string {
	s := strings.TrimSpace(remoteURL)
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")
	if s == "" {
		return ""
	}

	switch {
	case strings.Contains(s, "://"):
		// scheme://[user@]host/owner/repo
		s = s[strings.Index(s, "://")+3:]
		if at := strings.Index(s, "@"); at >= 0 {
			s = s[at+1:]
		}
		slash := strings.Index(s, "/")
		if slash < 0 {
			return ""
		}
		s = s[slash+1:]
	case strings.Contains(s, "@") && strings.Contains(s, ":"):
		// SCP-style: user@host:owner/repo
		s = s[strings.Index(s, "@")+1:]
		colon := strings.Index(s, ":")
		if colon < 0 {
			return ""
		}
		s = s[colon+1:]
	}

	parts := strings.Split(s, "/")
	if len(parts) < 2 {
		return ""
	}
	return strings.Join(parts[:len(parts)-1], "/")
}
