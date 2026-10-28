# giantmem

Desktop memory browser for your `giantmem` archive. It's a Wails app (Go backend, React+TS frontend) that binds the giantmem read layer directly — no IPC, no HTTP, no extra daemon. The same `internal/artifacts` and `internal/search` packages that power the CLI and MCP also power this window.

Six tabs (the last one only when do-it.nvim is installed):

1. **activity** — which repos and files moved recently, with per-project sparklines, a heatmap, the last 7 days' tool mix, and p50 active session time per project.
2. **sessions** — search and browse Claude Code sessions indexed in `archives.db`. Pick one and the right pane has four views. **transcript** unpacks the JSONL into a devtools-style log: collapsible turns, paired tool_use ↔ tool_result blocks, expand-all / collapse-all, either sort order. The **reports** toggle cuts it down to each turn's final report, the text the terminal showed when the turn ended (open edges, what got done), rendered as markdown with a copy button and the prompt that started the turn. The filter box searches within the reports, and the toggle sticks across sessions. A session with no model output says so, and a `/clear` stub links to the session it continued. **tools** opens with the session's tool mix, then lists every tool call it made, grouped by thread, so each subagent shows up under its type and description; click a call for its input, the head of its result, and a star toggle. **files** is the session's archived working set (scratchpad scripts, patches, drafts, background task output, workflow scripts) with a viewer, open, reveal-in-Finder, and **keep as snippet**, which copies the file into `snippets/` under the archive and logs its `giantmem://file` link in `snippets/sources.tsv`. **links** is what the session produced: claude.ai artifacts, vault notes (they open in Obsidian), handoffs, doit items, MRs. The header carries the first prompt, the bundle folder, and a copyable resume command. A transcript Claude Code has already pruned loads from the bundle's snapshot instead.
3. **day** — every session with tool activity on a given day, oldest first, with its first prompt, call and error counts, active time, files, and links. Click one to open it.
4. **artifacts** — hybrid search (FTS + vector + recency + access) and faceted browse over the typed `artifacts` projection in `live.db`. Filter by type, status, lifecycle, feature, and repo. Detail pane renders the markdown body.
5. **tools** — search tool calls across every session and subagent. Pick `Bash`, `Edit`, `WebFetch`, etc. (or `any tool`), type some words, tick **errors only** if you're hunting failures or **starred** for the calls you kept, and jump from any hit into its session.
6. **doit - todos** — the do-it.nvim lists, laid out like the tmux manager: lists on the left (active, open, empty), the picked list's items in the middle grouped by status and priority, and the item's details and notes on the right. Click an item's glyph or hit space to cycle pending → in progress → done → pending; `x` completes, `e` edits text, priority, due date, and notes, `d` twice deletes, and the box at the top of the middle column adds an item. The top search box matches list names, item text, item notes, and list notes, and keeps the list | item | details layout with the matches highlighted. It reads and writes the same JSON files nvim, tmux, and the doit MCP server use (`$DOIT_DATA_DIR/lists`, default `~/.local/share/nvim/doit/lists`), keeps owed twins in step the way the MCP server does, and picks up their edits within a couple of seconds. The tab stays hidden when that directory doesn't exist.

Deep links open the app straight onto what they name, which is how the vault daily note and doit items link back here:

| Link | Opens |
|---|---|
| `giantmem://session/<id>` | the session |
| `giantmem://day/<YYYY-MM-DD>` | the day tab on that date |
| `giantmem://tool/<id>/<main or agent id>/<seq>` | the session's tools view with that call expanded |
| `giantmem://file/<id>/<path in bundle>` | the session's files view with that file open (percent-encode spaces) |

The session header, every tool call, and every archived file have a "copy deep link" button, and the CLI (`--links`, `--json`) and MCP tools hand out the same links. The scheme is declared in `wails.json` and macOS picks it up once the app is in `/Applications`.

Read-only apart from starring tool calls (`starred_tool_uses` in `archives.db`) and keeping snippets. Writes (lifecycle changes, in-place editing) are deferred.

## Prerequisites

You'll need Go 1.21+, Node 18+, and the Wails CLI. Use **v2.15.0 or newer** and keep the CLI matched to the version in `gui/go.mod`. Older ones break in ways that look like your machine's fault: v2.9.1 has a parser bug on the cross-module `replace` directive under Go 1.26, and v2.12.0 cannot read Go 1.27 export data (`internal error: package "bufio" without types`) or link `UniformTypeIdentifiers` against the macOS 26 SDK.

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails version   # expect v2.15.0+
```

You also need the giantmem stack running normally — `live.db` and `archives.db` need to exist at `$GIANTMEM_ARCHIVE_BASE/` (default `~/giantmem_archive/`). Hybrid search calls the daemon's `embed` RPC for query vectors, so for best results have `giantmemd` up.

```bash
giantmem daemon status
ls -la ~/giantmem_archive/{live.db,archives.db}
```

If the daemon's down hybrid will silently fall back to FTS + recency + access (vector score collapses to zero); FTS, ListArtifacts, ListSessions, and the tools tab still work fine.

## Running

`make` is the short path. Manual `wails` calls still work.

```bash
cd giantmem/gui
make dev             # wails dev — live reload, opens a window
make build           # wails build — writes build/bin/giantmem.app
make install         # build + rsync into /Applications/giantmem.app
make reinstall       # install + kill running app + relaunch
```

Override install location: `make install INSTALL_TO=$HOME/Applications/giantmem.app`.

For a fresh-machine setup, the repo-root `make bootstrap` runs the GUI install plus the CLI, daemon, and session-sweep agent — see [../../INSTALL.md](../../INSTALL.md).

If you'd rather poke at the bindings from a browser, `wails dev` also exposes the Go methods at `http://localhost:34115` — open it and use the devtools console.

`wails build -skipbindings` is useful when you want to skip the parser pass (it's a sanity check more than a real shortcut — bindings regenerate quickly).

The output binary is `Giantmem` (set in `wails.json` as `outputfilename`). The `.app` bundle uses `build/appicon.png`, which is rendered from `build/appicon-src.png` (any square image works) into a rounded 896px tile on a transparent 1024 canvas. To swap the logo, replace the source and run:

```bash
magick build/appicon-src.png -resize 896x896 \
  \( -size 896x896 xc:none -fill white -draw "roundrectangle 0,0,895,895,200,200" \) \
  -alpha set -compose DstIn -composite \
  -compose Over -fill none -stroke '#30363d' -strokewidth 6 -draw "roundrectangle 3,3,892,892,197,197" \
  -background none -gravity center -extent 1024x1024 build/appicon.png
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

Tool calls, files, and links come from what `giantmem db ingest` indexed, so a session still running shows its calls as of the last ingest (SessionEnd and the 5-minute sweep keep that close). Results are stored as their first 500 chars; the transcript view has the full body. With an empty `tool_uses` index the tools tab falls back to stream-parsing every session JSONL, which is slower and can't see subagents.
