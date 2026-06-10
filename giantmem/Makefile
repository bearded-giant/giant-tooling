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

VPS_HOST ?= bryan@5.161.66.2
VPS_SRC  ?= ~/dev/giant-tooling/giantmem

.PHONY: all build install install-completions uninstall test clean tidy run deploy-vps

all: build

tidy:
	go mod tidy

build: tidy
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BIN) .

install: build install-completions
	mkdir -p $(INSTALL_DIR)
	install -m 0755 $(BIN) $(INSTALL_DIR)/$(BIN)
	@echo "installed -> $(INSTALL_DIR)/$(BIN)"

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

# Rebuild + install on the cloud-claude VPS. Source already mutagen-synced;
# this ssh's in and runs `make install` remotely. Assumes Go installed at
# ~/.local/go/bin and ~/.local/bin on PATH.
#
# Override host: make deploy-vps VPS_HOST=bryan@1.2.3.4
deploy-vps:
	@echo "==> deploying to $(VPS_HOST):$(VPS_SRC)"
	ssh $(VPS_HOST) 'export PATH=$$HOME/.local/bin:$$HOME/.local/go/bin:$$PATH && cd $(VPS_SRC) && make install'
	@echo "==> verifying"
	@ssh $(VPS_HOST) '$$HOME/.local/bin/giantmem version'
