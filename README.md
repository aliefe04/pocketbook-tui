# PocketBook Cloud TUI

A terminal UI for browsing and downloading your PocketBook Cloud library.

![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go)

## Features

- **Login with email & password** — Secure OAuth2 authentication
- **Browse your library** — Searchable list with title, author, format, and reading progress
- **Book details** — Full metadata, reading progress bar, and download option
- **Download books** — Save books to `~/pocketbook`
- **Keyboard-driven** — Fully navigable without a mouse

## Installation

### From source

```bash
go install github.com/aliefe/pocketbook-tui/cmd/pbtui@latest
```

### Build locally

```bash
git clone https://github.com/aliefe/pocketbook-tui.git
cd pocketbook-tui
go build -o pbtui ./cmd/pbtui
```

## Usage

```bash
pbtui
```

### Controls

| Key | Action |
|-----|--------|
| `Enter` | Confirm / Open details |
| `Esc` / `q` | Quit / Go back |
| `↑` / `↓` | Navigate |
| `/` | Filter books |
| `d` | Download book |
| `r` | Refresh library |
| `b` / `Backspace` | Back to library (from details) |

## Configuration

Config and tokens are stored in `~/.config/pocketbook-tui/config.json`.

Downloaded books are saved to `~/pocketbook/`.

## How it works

This tool uses the reverse-engineered PocketBook Cloud REST API via the excellent [`micronull/pocketbook-cloud-client`](https://github.com/micronull/pocketbook-cloud-client) Go library.

**API Base:** `https://cloud.pocketbook.digital/api/v1.0/`

## Tech Stack

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) — TUI framework
- [Bubbles](https://github.com/charmbracelet/bubbles) — UI components
- [Lipgloss](https://github.com/charmbracelet/lipgloss) — Styling
- [pocketbook-cloud-client](https://github.com/micronull/pocketbook-cloud-client) — API client

## Disclaimer

This is an unofficial tool. PocketBook Cloud API is not publicly documented and may change without notice. Use at your own risk.

## License

MIT
