BIN     := giantmem
PREFIX  ?= $(HOME)/.local
INSTALL_DIR := $(PREFIX)/bin

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
  -X github.com/bearded-giant/giant-tooling/giantmem/cmd.Version=$(VERSION) \
  -X github.com/bearded-giant/giant-tooling/giantmem/cmd.Commit=$(COMMIT) \
  -X github.com/bearded-giant/giant-tooling/giantmem/cmd.BuildDate=$(DATE)

.PHONY: all build install uninstall test clean tidy run

all: build

tidy:
	go mod tidy

build: tidy
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BIN) .

install: build
	mkdir -p $(INSTALL_DIR)
	install -m 0755 $(BIN) $(INSTALL_DIR)/$(BIN)
	@echo "installed -> $(INSTALL_DIR)/$(BIN)"

uninstall:
	rm -f $(INSTALL_DIR)/$(BIN)

test:
	go test ./...

clean:
	rm -f $(BIN)

run: build
	./$(BIN) $(ARGS)
