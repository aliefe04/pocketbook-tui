package tui

import (
	"github.com/aliefe/pocketbook-tui/internal/config"
	"github.com/charmbracelet/lipgloss"
)

var colorBackground = lipgloss.AdaptiveColor{Light: "#FAF8F2", Dark: "#171A20"}

func fixedColor(hex string) lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: hex, Dark: hex}
}
func applyTheme(p config.Preferences) {
	colorText = lipgloss.AdaptiveColor{Light: "#22201C", Dark: "#EDE7DA"}
	colorMuted = lipgloss.AdaptiveColor{Light: "#6B6459", Dark: "#A29A8B"}
	colorFaint = lipgloss.AdaptiveColor{Light: "#C3BAA8", Dark: "#4B463E"}
	colorSelection = lipgloss.AdaptiveColor{Light: "#EFE2C4", Dark: "#34302A"}
	colorBackground = lipgloss.AdaptiveColor{Light: "#FAF8F2", Dark: "#171A20"}
	switch p.Theme {
	case "dark":
		colorText = fixedColor("#E6E8ED")
		colorMuted = fixedColor("#A0A8B7")
		colorFaint = fixedColor("#455064")
		colorSelection = fixedColor("#29354A")
		colorBackground = fixedColor("#171A20")
	case "light":
		colorText = fixedColor("#20242A")
		colorMuted = fixedColor("#59636F")
		colorFaint = fixedColor("#C6CED8")
		colorSelection = fixedColor("#DDE8F5")
		colorBackground = fixedColor("#FAFBFD")
	case "sepia":
		colorText = fixedColor("#3B3025")
		colorMuted = fixedColor("#766452")
		colorFaint = fixedColor("#C5B79E")
		colorSelection = fixedColor("#E1D1AF")
		colorBackground = fixedColor("#F1E5CA")
	}
	accents := map[string]lipgloss.AdaptiveColor{
		"blue": {Light: "#245BB3", Dark: "#8DB5FA"}, "green": {Light: "#256A45", Dark: "#80C99D"}, "amber": {Light: "#8A5A0B", Dark: "#E2A65A"}, "purple": {Light: "#7437A3", Dark: "#C69BED"}, "rose": {Light: "#A6385C", Dark: "#EF9DB9"},
	}
	colorAccent = accents[p.Accent]
	if p.Theme == "dark" {
		colorAccent = fixedColor(colorAccent.Dark)
	} else if p.Theme != "auto" {
		colorAccent = fixedColor(colorAccent.Light)
	}
	colorReadText, colorReadDim = colorText, colorMuted
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	textStyle = lipgloss.NewStyle().Foreground(colorText)
	boldStyle = lipgloss.NewStyle().Bold(true).Foreground(colorText)
	mutedStyle = lipgloss.NewStyle().Foreground(colorMuted)
	labelStyle = lipgloss.NewStyle().Bold(true).Foreground(colorMuted)
	faintStyle = lipgloss.NewStyle().Foreground(colorFaint)
	accentStyle = lipgloss.NewStyle().Foreground(colorAccent)
	errorStyle = lipgloss.NewStyle().Bold(true).Foreground(colorError)
	successStyle = lipgloss.NewStyle().Foreground(colorSuccess)
	keyStyle = lipgloss.NewStyle().Bold(true).Foreground(colorText)
	progressFill = lipgloss.NewStyle().Foreground(colorAccent)
	progressEmpty = lipgloss.NewStyle().Foreground(colorFaint)
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(colorText).Background(colorSelection)
	selectedMeta = lipgloss.NewStyle().Foreground(colorMuted).Background(colorSelection)
	selectedMark = lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Background(colorSelection)
	readText = lipgloss.NewStyle().Foreground(colorReadText)
	readDim = lipgloss.NewStyle().Foreground(colorReadDim)
	readChapter = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
}
