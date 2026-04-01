# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

gcalsync is a Go CLI tool that syncs Google Calendar events across multiple accounts by creating "blocker" events (suffixed with `[gcalsync]`) in destination calendars. It is stateless — all sync state is derived from Google Calendar extended properties on blocker events, with no local database.

## Build & Run

```bash
go build -o gcalsync .    # Build (requires Go 1.23+)
nix build                 # Build via Nix flake
nix develop               # Development shell with Go toolchain
```

No automated tests exist. Manual testing against real Google Calendar accounts is required.

## Commands

```
gcalsync init                              # First-run setup
gcalsync sync [--dry-run] [-v]             # Sync all calendars
gcalsync desync [-v]                       # Remove all blocker events
gcalsync account add <name> [--enterprise] # Add account with OAuth
gcalsync account list                      # List accounts
gcalsync account update <name> --enterprise=false
gcalsync account refresh <name>            # Re-authenticate
gcalsync calendar add <account> <id>       # Add calendar
gcalsync calendar list
gcalsync calendar remove <account> <id>
gcalsync service install [--interval 15m]  # Install launchd service
gcalsync service uninstall
gcalsync service status
gcalsync service logs [--lines N]
```

## Architecture

Standard Cobra CLI layout with `internal/` packages for business logic.

| Package | Role |
|---------|------|
| `cmd/` | One file per command, Cobra wiring |
| `internal/config/` | TOML config structs, Load/Save/Validate |
| `internal/auth/` | OAuth2 flow, token persistence with auto-refresh write-back |
| `internal/gcal/` | Google Calendar API wrapper (ListEvents, ListBlockers, CRUD) |
| `internal/sync/` | Three-phase sync engine (Gather → Plan → Execute) |
| `internal/service/` | launchd plist generation and management |

### Sync Engine (`internal/sync/`)

- **events.go** — `ShouldSkip()` and `ShouldSkipForDest()` filtering logic
- **blocker.go** — `BuildBlockerEvent()` construction, instance ID generation, time formatting
- **diff.go** — `Diff()` compares desired blockers vs existing, emits creates/updates/deletes
- **engine.go** — `Engine.Run()` orchestrates the three phases, handles recurring event instances

### Key Design Decisions

- **Stateless**: Extended properties (`gcalsync_source_event_id`, `gcalsync_source_calendar_id`) on blocker events replace the old SQLite database
- **Ownership**: Blockers matched by `source_event_id` regardless of which calendar created them. Ownership transfer emitted as an Update during diff, applied during execute
- **Recurring events**: Parent recurring events create a single recurring blocker with copied `Recurrence` rules. Modified instances are updated via constructed instance IDs (`{parentID}_{compactDateTime}`)
- **Enterprise flag**: Manual per-account config flag; non-enterprise accounts skip `outOfOffice` blocker creation
- **Config precedence**: flag > env (`GCALSYNC_*`) > config file > defaults

### Blocker Event Properties

- Summary: `source.Summary + " [gcalsync]"`
- Color: Graphite (colorId `"8"`)
- Reminders: always disabled
- Visibility: configurable via `block_event_visibility`
- EventType: copies `outOfOffice` and `focusTime` only
- Attendees: destination calendar with source response status (omitted for OOO events)

## Config

Location: `~/.config/gcalsync/config.toml`
Tokens: `~/.config/gcalsync/<account>.token.json`

## Gotchas

- `outOfOffice` events cannot have attendees (API returns 400)
- Google Calendar returns different `event.Updated` timestamps for the same event across accounts — this is why ownership tracking via `source_calendar_id` exists
- Events with the same iCalUID share the same `event.Id` across calendars
- `ListBlockers` uses API `q` parameter for `[gcalsync]` plus client-side `strings.Contains` verification
- `SingleEvents(false)` returns recurring parents + modified instances separately
- Instance IDs follow `{parentEventId}_{YYYYMMDDTHHMMSSZ}` format (all-day: `{parentEventId}_{YYYYMMDD}`)
- `DeleteEvent` handles both 404 and 410 (already gone) gracefully
