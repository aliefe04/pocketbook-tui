package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/aliefe/pocketbook-tui/internal/tui"
)

func main() {
	app, err := tui.NewApp()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing app: %v\n", err)
		os.Exit(1)
	}

	session := tui.NewGraphicsSession(app)
	defer session.Close()

	opts := append([]tea.ProgramOption{tea.WithAltScreen()}, session.ProgramOptions()...)
	p := tea.NewProgram(session.Model(), opts...)
	if _, err := p.Run(); err != nil {
		_ = session.Close()
		fmt.Fprintf(os.Stderr, "Error running app: %v\n", err)
		os.Exit(1)
	}
}
