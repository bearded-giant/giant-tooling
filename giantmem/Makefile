BIN     := giantmem
PREFIX  ?= $(HOME)/.local
INSTALL_DIR := $(PREFIX)/bin
BASH_COMPLETION_DIR ?= $(shell brew --prefix 2>/dev/null)/etc/bash_completion.d
ZSH_COMPLETION_DIR  ?= $(shell brew --prefix 2>/dev/null)/share/zsh/site-functions

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
  -X github.com/bearded-giant/giant-tooling/giantmem/cmd.Version=$(VERSION) \
  -X github.com/bearded-giant/giant-tooling/giantmem/cmd.Commit=$(COMMIT) \
  -X github.com/bearded-giant/giant-tooling/giantmem/cmd.BuildDate=$(DATE)

DEVBOX_HOST ?= devbox

.PHONY: all build install install-completions uninstall test clean tidy run deploy-devbox

all: build

tidy:
	go mod tidy

build: tidy
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BIN) .

install: build install-completions
	mkdir -p $(INSTALL_DIR)
	install -m 0755 $(BIN) $(INSTALL_DIR)/$(BIN)
	@echo "installed -> $(INSTALL_DIR)/$(BIN)"
	@for p in $(HOME)/Library/LaunchAgents/*.plist; do \
		grep -q "<string>$(INSTALL_DIR)/$(BIN)</string>" "$$p" 2>/dev/null || continue; \
		l=$$(basename "$$p" .plist); \
		launchctl print "gui/$$(id -u)/$$l" >/dev/null 2>&1 || continue; \
		launchctl bootout "gui/$$(id -u)/$$l" && launchctl bootstrap "gui/$$(id -u)" "$$p" && echo "reloaded $$l"; \
	done

install-completions: build
	@if [ -d "$(BASH_COMPLETION_DIR)" ]; then \
		./$(BIN) completion bash > "$(BASH_COMPLETION_DIR)/giantmem" && \
		echo "installed bash completion -> $(BASH_COMPLETION_DIR)/giantmem"; \
	fi
	@if [ -d "$(ZSH_COMPLETION_DIR)" ]; then \
		./$(BIN) completion zsh > "$(ZSH_COMPLETION_DIR)/_giantmem" && \
		echo "installed zsh completion  -> $(ZSH_COMPLETION_DIR)/_giantmem"; \
	fi

uninstall:
	rm -f $(INSTALL_DIR)/$(BIN)
	rm -f $(BASH_COMPLETION_DIR)/giantmem $(ZSH_COMPLETION_DIR)/_giantmem

test:
	go test ./...

clean:
	rm -f $(BIN)

run: build
	./$(BIN) $(ARGS)

# Cross-build for the devbox, ship the binary and the user units, restart.
# Override host: make deploy-devbox DEVBOX_HOST=other-box
deploy-devbox: tidy
	@echo "==> cross-building linux/amd64 $(VERSION)"
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o /tmp/$(BIN)-linux .
	ssh $(DEVBOX_HOST) 'mkdir -p ~/.local/bin ~/.config/systemd/user ~/.cache/giantmem'
	scp -q /tmp/$(BIN)-linux $(DEVBOX_HOST):.local/bin/$(BIN).new
	scp -q systemd/giantmemd.service systemd/giantmem-ingest.service systemd/giantmem-ingest.timer $(DEVBOX_HOST):.config/systemd/user/
	ssh $(DEVBOX_HOST) 'mv -f ~/.local/bin/$(BIN).new ~/.local/bin/$(BIN) && pkill -xf ".*/giantmem daemon serve" || true; systemctl --user daemon-reload && systemctl --user enable --now giantmem-ingest.timer && systemctl --user enable giantmemd && systemctl --user restart giantmemd && sleep 1 && systemctl --user is-active giantmemd && ~/.local/bin/$(BIN) version | head -1'
	@rm -f /tmp/$(BIN)-linux
