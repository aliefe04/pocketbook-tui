# PocketBook Cloud TUI

A terminal UI for browsing your PocketBook Cloud library, downloading books, and reading EPUB and TXT files in the terminal.

## Features

- **Sign in with email and PocketBook password** through the PocketBook Cloud API. The session is stored locally.
- **Browse your library** with the most recently read books first. Cloud bookmarks and local saves determine reading order. Filter by title or author.
- **Book details** with metadata and a progress bar. Scroll the details in short terminals.
- **Download books** to a local cache directory.
- **Read EPUB and TXT** as text in the terminal. Resume from a Cloud bookmark or a saved local position.
- **Native EPUB Cloud positions** with fresh reads, confirmed saves, and choices when another device changes the bookmark.
- **Light and dark terminals**: colors follow the terminal background.


## Requirements

- Go 1.26.1 or newer (the version in `go.mod`)
- A PocketBook Cloud account, with its email address and PocketBook password
- A terminal of at least 20x8 cells. The layouts are built for 40x14 and 80x24.

## Build

```bash
git clone https://github.com/aliefe04/pocketbook-tui.git
cd pocketbook-tui
go build -o pbtui ./cmd/pbtui
./pbtui
```

The Go module path is `github.com/aliefe/pocketbook-tui`.

## Signing in

The TUI keeps its own session. Signing in to PocketBook Cloud in a browser, including through Zen or with Google, does not sign the TUI in.

The TUI signs in with the email and PocketBook password of your PocketBook Cloud account. Follow these steps on first launch:

1. Enter your email, then press `enter`.
2. Choose your account's provider with `↑` / `↓` or `tab`, then press `enter`.
3. Enter your PocketBook password, then press `enter`.

The library opens after a successful sign-in. If sign-in fails, press `enter` to start again.

## Controls

### Sign-in

| Key | Action |
|-----|--------|
| `enter` | Continue to the next step, or retry after an error |
| `↑` / `↓` / `tab` | Choose a provider |
| `esc` / `ctrl+c` | Quit |

### Library

| Key | Action |
|-----|--------|
| `j` / `k`, `↑` / `↓` | Move the selection |
| `r` | Read the selected book |
| `enter` | Open the book's details |
| `d` | Download the selected book to the cache |
| `/` | Start a new filter |
| `R` | Refresh the library. The filter stays in place |
| `L` | Log out: clear the saved session and return to sign-in |
| `q` / `esc` / `ctrl+c` | Quit |

Filter notes:

- The filter matches titles and authors, and ignores case.
- Type to narrow the list. Press `backspace` to delete one character.
- Press `enter` or `esc` to keep the filter and stop typing.
- Press `/` to start a new, empty filter.

### Details

| Key | Action |
|-----|--------|
| `r` | Read the book |
| `d` | Download the book |
| `j` / `k`, `↑` / `↓` | Scroll the details one row |
| `PgDn` / `PgUp` | Scroll the details one page |
| `esc` / `b` / `backspace` | Back to the library |
| `q` / `ctrl+c` | Quit |

The TUI hides fields without a value. It shows DRM and LCP only when they are set. It shows a scroll hint when the details do not fit.

### Resume choice

When Cloud and local positions differ, the TUI shows both sources and their saved percentages. Cloud is selected first.

| Key | Action |
|-----|--------|
| `↑` / `↓`, `k` / `j` | Choose Cloud or Local |
| `enter` | Open the chosen position |
| `esc` / `q` | Cancel and return to the previous screen without saving |
| `ctrl+c` | Quit without saving |

### Reader

| Key | Action |
|-----|--------|
| `j` / `k` | Scroll one displayed row |
| `↑` / `↓` | Turn a page in page mode, or scroll one row in scroll mode |
| `d` / `u`, `PgDn` / `PgUp`, `→` / `←` | Next / previous page |
| `f` / `space`, `b` | Next / previous page |
| `P` | Switch between page and scroll modes |
| `n` / `p` | Next / previous chapter |
| `g` / `G` | Start / end of the chapter |
| `?` | Show or hide the control list |
| `↑` / `↓`, `PgUp` / `PgDn` inside help | Scroll the complete controls and Cloud status without moving the book |
| `q` / `esc` inside help | Return to reading without saving |
| `C` | Check Cloud again, or review a changed Cloud position |
| `S` | Open settings from the reader, library, or book details |
| `I` | View an embedded photo without changing the reading position |
| `q` / `esc` | Save locally and sync supported EPUB positions. Return after Cloud confirms |
| `ctrl+c` | Quit without saving the position |

If the local save fails, the reader stays open. Nothing is sent to Cloud. Press `q` to retry the local save.

Cloud failures and changed bookmarks keep the reader open with these choices:

| Key | Action |
|-----|--------|
| `o` | Overwrite a changed Cloud bookmark with this passage, after another fresh check |
| `c` | Load the changed Cloud bookmark |
| `r` | Retry a failed Cloud sync |
| `l` | Keep the position on this device only and return to the library |
| `enter` | Keep reading without sending a position |
| `ctrl+c` | Quit without another save or Cloud write |

Small terminals show compact choices. The `↵` symbol means `enter`.

## Customization

Settings shortcuts appear in the library, book-detail, and reader bars, including compact terminals.
Important save notices remain visible when the reader bars are hidden.

Press `S` to open settings. Changes stay in a draft until you press `s` to save.
Press `esc` to discard the draft. Press `r` to reset the draft to defaults.
Use `↑`/`↓` to select a setting, `←`/`→` to change it, and `tab` to reach the next category.

| Category | Settings |
|----------|----------|
| Appearance | Auto, dark, light, or sepia theme. Blue, green, amber, purple, or rose accent |
| Reading | Page or scroll mode. Reading width, side margins, top/bottom margins, line spacing, paragraph spacing, left alignment or justification |
| Reading information | Header and footer visibility. Percentage, terminal page count, both, or neither. Page overlap |
| Images | Color, grayscale, captions only, or hidden. Auto/native graphics or portable blocks. Auto height, or 4 to 60 rows |
| Controls | Mouse wheel. Separate custom keys for next page, previous page, line down, and line up |
| Library | Recent reading, title, or author order. Compact one-row entries |
| Cloud | Automatic checks on opening. Sync on exit. Request timeout from 5 to 120 seconds |

For a custom key, select its setting, press `enter`, then press the new key.
Settings reject duplicate keys and keys reserved for safe exits, Cloud recovery, and chapter navigation.
Page overlap repeats selected rows when you turn a page.

Disabling automatic Cloud checks still permits manual `C` checks.
Disabling Cloud sync keeps reading positions on this device only, including during conflict recovery.
Layout changes preserve the source passage. Page counts describe the current terminal layout.

Preferences live in `~/.config/pocketbook-tui/preferences.json`, separate from login and reading-position files.
Invalid preferences show an error without replacing the file.
Change font family and font size in your terminal application.


## Books and reading

- **Formats:** The reader supports EPUB and TXT only. It rejects other formats.
- **DRM:** The reader rejects books marked DRM or LCP.
- **Reader output:** The reader displays text and embedded PNG, JPEG, and GIF photos. It does not reproduce fonts or the device's page layout.
- **Page turns:** Page movement follows displayed rows, including wrapped paragraphs and chapter boundaries. It does not skip the rest of a long paragraph.
- **Resizing:** The current source passage stays visible when the terminal width changes.
- **Saved positions:** Local positions identify paragraphs or text boundaries. Cloud positions can identify a paragraph or an image, not a terminal-specific page boundary.
- **Cloud bookmarks:** The reader resolves supported EPUB location markers, called CFI, to their paragraph or image. It does not restore a device's exact page layout.
- **Approximate resume:** If a bookmark cannot be resolved, the reader estimates the position from the Cloud percentage. It labels this estimate.
- **Reading percentages:** The terminal percentage counts text lines, so it can differ from the Cloud percentage. The resume notice shows the saved Cloud percentage.
- **Local positions:** The reader saves locally before any Cloud write. TXT positions remain local-only.
- **Unmapped passages:** If the reader cannot map a passage to an exact EPUB location, it saves locally and shows a warning.
- **Earlier positions:** The reader converts older local positions when you open their books. It saves the current format when you leave the reader.


### Embedded photos

The reader checks native image support at startup with a short terminal query.
Compatible terminals display full-resolution images through the [Kitty graphics protocol](https://sw.kovidgoyal.net/kitty/graphics-protocol/).
Native placement preserves the image's pixels instead of reducing its labels to colored text cells.

Other terminals use portable colored blocks with area averaging when shrinking images.
This fallback has fewer pixels, so small diagram labels can remain unreadable.
Choose captions-only mode if you do not want block previews.

Inline height defaults to Auto, which follows the reading width and image aspect ratio.
An explicit saved height remains unchanged.
Use settings to select Auto/native rendering or force portable blocks.


Press `I` to open the photo viewer. Press `n`/`p` to change photos, `+`/`-` to zoom, and arrow keys to pan.
Press `0` to fit the photo again. Press `esc`, `q`, or `I` to return to reading without saving or syncing.
The viewer also works when inline photos are hidden.

Press `O` inside the viewer to open the original image in your operating system's image viewer.
The reader creates a private temporary PNG and keeps your reading position unchanged.
It removes its temporary exports when you quit.

The reader accepts embedded PNG, JPEG, and GIF resources. GIF display uses the first frame.
It does not fetch remote images or render SVG vectors.
Color and grayscale modes explain unavailable images while keeping following text readable.

Image resources have limits of 8 MiB each, 32 MiB combined, and 8 million decoded pixels per image.
The reader reports images above these limits instead of allocating their full decoded size.
Cloud bookmarks can name an image exactly. Existing local positions still identify a paragraph or text boundary.

## Cloud synchronization

Opening a cached EPUB shows the last known Cloud bookmark or local save without waiting for the network.
The reader checks Cloud in the background, with a default 30-second request deadline. Change this deadline in settings.
Different cached Cloud and local positions produce a choice, with Cloud selected first.

If Cloud times out, reading stays available. Press `C` to retry.
If Cloud reports another position, press `C` to review it.
The refresh does not move you or send a position.

The reader shows a short Cloud status above the text. Press `?` to see the full status.

Leaving with `q` or `esc` saves locally, checks the current Cloud bookmark, then sends an exact EPUB location.
The TUI reports `Synced to Cloud` only after a fresh read confirms both native pointers and the percentage.
It syncs when you leave the reader, not after every page.

Closing an unchanged, exact Cloud resume checks Cloud without writing.
This preserves the original character offset, percentage, and update time.
An unchanged approximate resume never sends its estimated position.

Earlier passages are valid saves. The TUI does not keep the highest percentage.
If either native pointer or the percentage changed elsewhere, the reader asks before overwriting it.

These requests cannot atomically compare and save.
A simultaneous device write can still occur between the check and save.
Close and sync one reader before continuing on another device.

Expired TUI sessions require signing in again.
Choose `l` to leave the reader, then use `L` in the library and sign in.
Browser sessions do not renew the TUI token.

### Verse device check

Use a disposable, non-DRM EPUB for device checks.
The dedicated development book is `pocketbook-tui-sync-test.epub`, with `SYNC_EARLY_*`, `SYNC_MIDDLE_*`, and `SYNC_LATE_*` paragraph markers.
Compare the marked paragraph, not just its percentage.

1. Sign in to the same PocketBook Cloud account on the TUI and Verse.
2. In the TUI, move until `SYNC_LATE_12` is the first paragraph on screen.
3. Press `q` and wait for `Synced to Cloud`.
4. Sync Cloud on Verse, then close and reopen the test book.
5. Confirm Verse opens at `SYNC_LATE_12`.
6. On Verse, move back to `SYNC_EARLY_03`, close the book, and sync Cloud.
7. Open the test book in the TUI. Choose Cloud if asked.
8. Confirm the first paragraph is `SYNC_EARLY_03`.

Repeat with a different marked paragraph in the iOS app to check that client's bookmarks.
Physical Verse and iOS results remain unverified until these device checks pass.


## Local files

| What | Where |
|------|-------|
| Session (token, refresh token, provider, shop ID) | `~/.config/pocketbook-tui/config.json` (created with mode `0600`) |
| Preferences | `~/.config/pocketbook-tui/preferences.json` (atomic saves, mode `0600`) |
| Downloaded books | `~/.config/pocketbook/` |
| Reading positions | `~/.config/pocketbook-tui/positions/<book-hash>.json` |

Downloading a book overwrites the cached file of the same name. Opening a book uses the cached copy when it exists, and downloads it otherwise.

Cached files use the server's file name. When the server gives no name, the file is `<book-hash>.<format>`.

## Limitations

- Native EPUB Cloud saves are implemented. Physical Verse and iOS interoperability still needs device verification.
- The reader does not support all PocketBook formats. Terminal-cell photos have lower detail than native graphics.

## How it works

This tool uses the [PocketBook Cloud REST API](https://cloud.pocketbook.digital/api/v1.0/) through the [`micronull/pocketbook-cloud-client`](https://github.com/micronull/pocketbook-cloud-client) Go library.
Native bookmark requests use the same endpoints as [PocketBook's web reader](https://cloud.pocketbook.digital/reader_new/).
The API is not publicly documented and may change without notice.

## Tech stack

- [Bubble Tea](https://github.com/charmbracelet/bubbletea): terminal UI framework
- [Bubbles](https://github.com/charmbracelet/bubbles): text input
- [Lipgloss](https://github.com/charmbracelet/lipgloss): styling and adaptive colors
- [pocketbook-cloud-client](https://github.com/micronull/pocketbook-cloud-client): API client

## Disclaimer

This is an unofficial tool. It is not affiliated with PocketBook. Use it at your own risk.

## License

This repository has no LICENSE file yet.
