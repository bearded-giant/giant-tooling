APP        := Giantmem
BUILD_BIN  := build/bin/$(APP).app
INSTALL_TO ?= /Applications/$(APP).app

# Version base comes from wails.json info.productVersion. Dev builds append
# +<short-sha>[.dirty]. Both are injected into main.Version via -ldflags so the
# in-app footer matches what was actually built.
VERSION_BASE = $(shell grep -E '"productVersion"' wails.json | head -1 | sed -E 's/.*"productVersion":[[:space:]]*"([^"]+)".*/\1/')
DEV_SHA      = $(shell git rev-parse --short=7 HEAD 2>/dev/null || echo nogit)
DEV_DIRTY    = $(shell git diff --quiet HEAD 2>/dev/null || echo .dirty)
DEV_VERSION  = $(VERSION_BASE)-dev+$(DEV_SHA)$(DEV_DIRTY)
LDFLAGS_REL  = -X main.Version=$(VERSION_BASE)
LDFLAGS_DEV  = -X main.Version=$(DEV_VERSION)

.PHONY: all help build build-dev run dev install install-dev reinstall reinstall-dev version clean

all: build

help:
	@echo "$(APP) - make targets"
	@echo ""
	@echo "  make build         wails build (release, version=$(VERSION_BASE))"
	@echo "  make build-dev     wails build with -dev+<sha>[.dirty] suffix"
	@echo "  make install       build + rsync into $(INSTALL_TO)"
	@echo "  make install-dev   build-dev + rsync into $(INSTALL_TO)"
	@echo "  make reinstall     install + relaunch app"
	@echo "  make reinstall-dev install-dev + relaunch app"
	@echo "  make dev           wails dev (hot-reload)"
	@echo "  make version       print the dev version that install-dev would use"
	@echo "  make clean         remove build artifacts"

version:
	@echo "$(DEV_VERSION)"

build:
	@echo "Building $(APP) $(VERSION_BASE)"
	wails build -ldflags "$(LDFLAGS_REL)"
	@echo "built $(APP) $(VERSION_BASE)"

build-dev:
	@echo "Building $(APP) $(DEV_VERSION)"
	wails build -ldflags "$(LDFLAGS_DEV)"
	@echo "built $(APP) $(DEV_VERSION)"

run: dev
dev:
	wails dev

install: build
	@if [ ! -d "$(BUILD_BIN)" ]; then \
		echo "build output missing: $(BUILD_BIN)"; exit 1; \
	fi
	rsync -a --delete $(BUILD_BIN)/ $(INSTALL_TO)/
	@echo "installed $(APP) $(VERSION_BASE) -> $(INSTALL_TO)"

install-dev: build-dev
	@if [ ! -d "$(BUILD_BIN)" ]; then \
		echo "build output missing: $(BUILD_BIN)"; exit 1; \
	fi
	rsync -a --delete $(BUILD_BIN)/ $(INSTALL_TO)/
	@echo "installed $(APP) $(DEV_VERSION) -> $(INSTALL_TO)"

reinstall: install
	@pkill -x $(APP) 2>/dev/null || true
	@sleep 0.3
	open $(INSTALL_TO)
	@echo "relaunched $(APP) $(VERSION_BASE)"

reinstall-dev: install-dev
	@pkill -x $(APP) 2>/dev/null || true
	@sleep 0.3
	open $(INSTALL_TO)
	@echo "relaunched $(APP) $(DEV_VERSION)"

clean:
	rm -rf build/bin
