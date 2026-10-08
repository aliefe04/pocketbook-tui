package tui

import "github.com/charmbracelet/lipgloss"

// Palette. Every color has a light and a dark variant; lipgloss picks the one
// matching the terminal background, so the same theme reads well on both.
// Accent is a restrained warm amber used for the title, selection marker,
// and progress fill. Everything else is neutral.
var (
	colorAccent    = lipgloss.AdaptiveColor{Light: "#8A5A0B", Dark: "#E2A65A"}
	colorText      = lipgloss.AdaptiveColor{Light: "#22201C", Dark: "#EDE7DA"}
	colorMuted     = lipgloss.AdaptiveColor{Light: "#6B6459", Dark: "#A29A8B"}
	colorFaint     = lipgloss.AdaptiveColor{Light: "#C3BAA8", Dark: "#4B463E"}
	colorSelection = lipgloss.AdaptiveColor{Light: "#EFE2C4", Dark: "#34302A"}
	colorSuccess   = lipgloss.AdaptiveColor{Light: "#2D6A46", Dark: "#86C79F"}
	colorError     = lipgloss.AdaptiveColor{Light: "#A3281C", Dark: "#F09A8E"}

	// Reader palette: paper-like body text with sepia chrome.
	colorReadText = lipgloss.AdaptiveColor{Light: "#2E2619", Dark: "#D9C9A3"}
	colorReadDim  = lipgloss.AdaptiveColor{Light: "#8A7C62", Dark: "#7A6F5A"}
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	textStyle     = lipgloss.NewStyle().Foreground(colorText)
	boldStyle     = lipgloss.NewStyle().Bold(true).Foreground(colorText)
	mutedStyle    = lipgloss.NewStyle().Foreground(colorMuted)
	labelStyle    = lipgloss.NewStyle().Bold(true).Foreground(colorMuted)
	faintStyle    = lipgloss.NewStyle().Foreground(colorFaint)
	accentStyle   = lipgloss.NewStyle().Foreground(colorAccent)
	errorStyle    = lipgloss.NewStyle().Bold(true).Foreground(colorError)
	successStyle  = lipgloss.NewStyle().Foreground(colorSuccess)
	keyStyle      = lipgloss.NewStyle().Bold(true).Foreground(colorText)
	progressFill  = lipgloss.NewStyle().Foreground(colorAccent)
	progressEmpty = lipgloss.NewStyle().Foreground(colorFaint)

	// Selected list rows: a full-width band with an accent marker.
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(colorText).Background(colorSelection)
	selectedMeta  = lipgloss.NewStyle().Foreground(colorMuted).Background(colorSelection)
	selectedMark  = lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Background(colorSelection)

	// Reader chrome and body.
	readText    = lipgloss.NewStyle().Foreground(colorReadText)
	readDim     = lipgloss.NewStyle().Foreground(colorReadDim)
	readChapter = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
)
