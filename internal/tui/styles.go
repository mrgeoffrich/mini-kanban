package tui

import "github.com/charmbracelet/lipgloss"

// Shared styles. Kept in one place so palette tweaks land everywhere.
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("57")).
			Padding(0, 1)
	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Padding(0, 1)
	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Padding(0, 1)
	mutedStyle = lipgloss.NewStyle().Foreground(mutedColor)
	keyStyle   = lipgloss.NewStyle().Foreground(cardKeyColor)
	boldStyle  = lipgloss.NewStyle().Bold(true)

	tabActive = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("57")).
			Padding(0, 2)
	tabInactive = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Padding(0, 2)
	tabSep = lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Render("·")
)

// repoGlyph prefixes the repo name in the header. Defaults to the Powerline
// / Nerd Font git-branch glyph (U+E0A0) since most dev terminals carry it.
// If your terminal renders a tofu box, swap to a simpler choice like "⎇" or
// "git:".
const repoGlyph = ""

var (
	colBorder        = lipgloss.RoundedBorder()
	colBorderColor   = lipgloss.Color("240")
	colFocusBorder   = lipgloss.Color("212")
	colHeaderFocus   = lipgloss.Color("212")
	colHeaderUnfocus = lipgloss.Color("240")
	cardSelectedBG   = lipgloss.Color("57")
	cardKeyColor     = lipgloss.Color("147")
	mutedColor       = lipgloss.Color("241")
)
