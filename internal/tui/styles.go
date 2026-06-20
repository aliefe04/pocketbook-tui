package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors
	PrimaryColor   = lipgloss.Color("#E8A838")
	SecondaryColor = lipgloss.Color("#6B7280")
	SuccessColor   = lipgloss.Color("#10B981")
	ErrorColor     = lipgloss.Color("#EF4444")
	TextColor      = lipgloss.Color("#F3F4F6")
	DimColor       = lipgloss.Color("#9CA3AF")
	BgColor        = lipgloss.Color("#111827")
	BorderColor    = lipgloss.Color("#374151")

	// Reader-specific colors — warm paperback palette
	ReadingColor    = lipgloss.Color("#D9C9A3") // warm parchment text
	ReadingDimColor = lipgloss.Color("#7A6F5A") // muted sepia for rules
	ChapterColor    = lipgloss.Color("#E8A838") // gold for chapter titles

	// Styles
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(PrimaryColor).
			MarginLeft(2).
			MarginBottom(1)

	SubtitleStyle = lipgloss.NewStyle().
			Foreground(DimColor).
			MarginLeft(2).
			MarginBottom(1)

	BoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(BorderColor).
			Padding(1, 2).
			Margin(1, 2)

	ErrorStyle = lipgloss.NewStyle().
			Foreground(ErrorColor).
			Bold(true)

	SuccessStyle = lipgloss.NewStyle().
			Foreground(SuccessColor).
			Bold(true)

	HelpStyle = lipgloss.NewStyle().
			Foreground(DimColor).
			MarginTop(1)

	ListTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(PrimaryColor)

	SelectedItemStyle = lipgloss.NewStyle().
			Foreground(TextColor).
			Bold(true)

	ItemStyle = lipgloss.NewStyle().
			Foreground(TextColor)

	DimColorStyle = lipgloss.NewStyle().
			Foreground(DimColor)

	DetailLabelStyle = lipgloss.NewStyle().
			Foreground(DimColor).
			Bold(true)

	DetailValueStyle = lipgloss.NewStyle().
			Foreground(TextColor)

	ProgressBarStyle = lipgloss.NewStyle().
			Foreground(SuccessColor)

	ProgressBarEmptyStyle = lipgloss.NewStyle().
			Foreground(BorderColor)
)
