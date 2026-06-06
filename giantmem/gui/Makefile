APP        := Giantmem
BUILD_BIN  := build/bin/$(APP).app
INSTALL_TO ?= /Applications/$(APP).app

.PHONY: all build run install dev clean reinstall

all: build

# build the .app bundle (release build, signs locally)
build:
	wails build

# launch wails dev (hot-reload frontend, attached webview)
run: dev
dev:
	wails dev

# rsync the built bundle into /Applications. Default target overrides via
# INSTALL_TO=...  e.g. make install INSTALL_TO=$$HOME/Applications/$(APP).app
install: build
	@if [ ! -d "$(BUILD_BIN)" ]; then \
		echo "build output missing: $(BUILD_BIN)"; exit 1; \
	fi
	rsync -a --delete $(BUILD_BIN)/ $(INSTALL_TO)/
	@echo "installed -> $(INSTALL_TO)"

# build + install + relaunch a running copy (if any). Closes the app first
# so the new binary loads; users keep their window position via Wails state.
reinstall: install
	@pkill -x $(APP) 2>/dev/null || true
	@sleep 0.3
	open $(INSTALL_TO)
	@echo "relaunched $(APP)"

clean:
	rm -rf build/bin
