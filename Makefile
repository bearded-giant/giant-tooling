# giant-tooling — top-level installer
#
# Bootstrap path for a fresh machine:
#   make bootstrap
#
# Sub-targets exist if you want piecemeal installs. See `make help`.

SHELL := /bin/bash

GIANTMEM_DIR  := giantmem
GUI_DIR       := giantmem/gui
LAUNCH_AGENTS := $(HOME)/Library/LaunchAgents
SWEEP_PLIST   := com.giantmem-session-sweep.plist

.PHONY: help bootstrap check-prereqs cli gui daemon-install \
        session-sweep session-sweep-unload first-run \
        uninstall-session-sweep

help:
	@echo "giant-tooling installer"
	@echo
	@echo "  make bootstrap         full install: prereqs + cli + gui + daemon + sweep + first-run"
	@echo
	@echo "Piecemeal:"
	@echo "  make check-prereqs     verify go / node / wails / sqlite on PATH"
	@echo "  make cli               build + install giantmem CLI -> ~/.local/bin/giantmem"
	@echo "  make gui               build + install Giantmem.app -> /Applications/"
	@echo "  make daemon-install    register giantmemd LaunchAgent + start"
	@echo "  make session-sweep     install + load 5-min session-ingest LaunchAgent"
	@echo "  make first-run         backfill live.db + ingest existing session JSONLs"
	@echo
	@echo "Cleanup:"
	@echo "  make uninstall-session-sweep"
	@echo
	@echo "After bootstrap, install writer hooks from the claude-code-config repo:"
	@echo "  https://github.com/bearded-giant/claude-code-config  (stow -> ~/.claude)"

bootstrap: check-prereqs cli gui daemon-install session-sweep first-run
	@echo
	@echo "== bootstrap complete =="
	@echo
	@echo "Next: install writer hooks from claude-code-config (separate repo)."
	@echo "Without them, live.db won't capture Claude PostToolUse writes — only"
	@echo "out-of-band edits caught by 'giantmem index backfill' / daemon sweeps."

check-prereqs:
	@command -v go      >/dev/null || { echo "missing: go (https://go.dev/dl/)"; exit 1; }
	@command -v node    >/dev/null || { echo "missing: node (brew install node)"; exit 1; }
	@command -v wails   >/dev/null || { echo "missing: wails (go install github.com/wailsapp/wails/v2/cmd/wails@latest)"; exit 1; }
	@command -v sqlite3 >/dev/null || { echo "missing: sqlite3 (system)"; exit 1; }
	@case ":$$PATH:" in *":$$HOME/.local/bin:"*) ;; \
	  *) echo "warn: ~/.local/bin not on PATH; add it to your shell rc"; ;; esac
	@echo "prereqs ok"

cli:
	$(MAKE) -C $(GIANTMEM_DIR) install

gui:
	$(MAKE) -C $(GUI_DIR) install

daemon-install:
	@if [ ! -x "$$HOME/.local/bin/giantmem" ]; then \
		echo "giantmem CLI not installed yet — run 'make cli' first"; exit 1; \
	fi
	$$HOME/.local/bin/giantmem daemon install

# Install the 5-min session-sweep LaunchAgent. We materialize a copy with
# $HOME substituted so the same plist works for any user; the in-repo
# template uses hardcoded /Users/bryan paths as a reference.
session-sweep:
	@mkdir -p $(LAUNCH_AGENTS)
	@sed -e "s|/Users/bryan|$$HOME|g" \
	    -e "s|com.bryan.giantmem-session-sweep|com.giantmem-session-sweep|g" \
	    $(GIANTMEM_DIR)/launchd/com.bryan.giantmem-session-sweep.plist \
	    > $(LAUNCH_AGENTS)/$(SWEEP_PLIST)
	@launchctl unload $(LAUNCH_AGENTS)/$(SWEEP_PLIST) 2>/dev/null || true
	@launchctl load   $(LAUNCH_AGENTS)/$(SWEEP_PLIST)
	@echo "session-sweep loaded -> $(LAUNCH_AGENTS)/$(SWEEP_PLIST)"

uninstall-session-sweep:
	@launchctl unload $(LAUNCH_AGENTS)/$(SWEEP_PLIST) 2>/dev/null || true
	@rm -f $(LAUNCH_AGENTS)/$(SWEEP_PLIST)
	@echo "session-sweep removed"

first-run:
	@if [ ! -x "$$HOME/.local/bin/giantmem" ]; then \
		echo "giantmem CLI not installed yet — run 'make cli' first"; exit 1; \
	fi
	$$HOME/.local/bin/giantmem index backfill
	$$HOME/.local/bin/giantmem ingest --sessions-only
