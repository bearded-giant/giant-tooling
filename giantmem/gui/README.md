# Giantmem

Desktop memory browser for your `giantmem` archive. It's a Wails app (Go backend, React+TS frontend) that binds the giantmem read layer directly — no IPC, no HTTP, no extra daemon. The same `internal/artifacts` and `internal/search` packages that power the CLI and MCP also power this window.

Three tabs:

1. **artifacts** — hybrid search (FTS + vector + recency + access) and faceted browse over the typed `artifacts` projection in `live.db`. Filter by type, status, lifecycle, feature, and repo. Detail pane renders the markdown body.
2. **sessions** — search and browse Claude Code session transcripts indexed in `archives.db`. Pick a session and the right pane unpacks the JSONL into a devtools-style transcript: collapsible turns, paired tool_use ↔ tool_result blocks, expand-all / collapse-all, oldest-first or newest-first sort.
3. **tools** — search every tool_use across every session. Pick `Bash`, `Edit`, `WebFetch`, etc. (or `any tool`), type a substring, and you get a hit per tool invocation with the input JSON, the paired tool_result body, and a one-click jump into the session viewer at that turn.

Read-only for v1. Writes (lifecycle changes, in-place editing) are deferred.

## Prerequisites

You'll need Go 1.21+, Node 18+, and the Wails CLI. The version of Wails that works for this project is **v2.12.0 or newer** — v2.9.1 has a parser bug that trips on the cross-module `replace` directive under Go 1.26.

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails version   # expect v2.12.0+
```

You also need the giantmem stack running normally — `live.db` and `archives.db` need to exist at `$GIANTMEM_ARCHIVE_BASE/` (default `~/giantmem_archive/`). Hybrid search calls the daemon's `embed` RPC for query vectors, so for best results have `giantmemd` up.

```bash
giantmem daemon status
ls -la ~/giantmem_archive/{live.db,archives.db}
```

If the daemon's down hybrid will silently fall back to FTS + recency + access (vector score collapses to zero); FTS, ListArtifacts, ListSessions, and the tools tab still work fine.

## Running

```bash
cd giantmem/gui
wails dev            # live reload, opens a window
```

If you'd rather poke at the bindings from a browser, `wails dev` also exposes the Go methods at `http://localhost:34115` — open it and use the devtools console.

## Building a `.app`

```bash
cd giantmem/gui
wails build          # writes build/bin/Giantmem.app
open build/bin/Giantmem.app
```

`wails build -skipbindings` is useful when you want to skip the parser pass (it's a sanity check more than a real shortcut — bindings regenerate quickly).

The output binary is `Giantmem` (set in `wails.json` as `outputfilename`). The `.app` bundle uses `build/appicon.png`, which is rendered from `build/appicon.svg` if you want to swap the logo:

```bash
rsvg-convert -w 1024 -h 1024 build/appicon.svg -o build/appicon.png
```

## Module layout

The GUI lives in its own Go module (`github.com/bearded-giant/giant-tooling/giantmem/gui`) but pulls types and read functions from the parent `giantmem` module. Two mechanisms keep that working:

1. `gui/go.mod` has `replace github.com/bearded-giant/giant-tooling/giantmem => ../`
2. `giantmem/go.work` lists both modules so `go build ./...` resolves them as workspace siblings.

Either alone would be enough; having both lets you build from either dir and stops wails' bindings parser from getting confused by the cross-module hop.

## Configuration

The app reads `GIANTMEM_ARCHIVE_BASE` from the environment exactly like the CLI does (defaults to `~/giantmem_archive`). Window size, sidebar width, and the session-turn sort preference persist across launches via `localStorage` keys `gm.win`, `gm.sidebarWidth`, `gm.turnOrder`.

## Keyboard

`/` focuses the search input. `j` / `k` move row selection. `Esc` clears the current selection (or blurs the search field if it's focused).

## Known limitations

The archive FTS body keeps only the first 20 Bash commands per session, each clipped at 150 chars, and skips other tool inputs and every tool_result entirely. The tools tab works around that by stream-parsing the JSONL when you search. Toggle off **FTS pre-filter** in the topbar if you want it to scan every session jsonl (slower, but it'll find tool calls that aren't visible to FTS).
