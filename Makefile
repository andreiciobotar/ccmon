.PHONY: install up build test test-factory snapshot screenshot clean

BIN := bin/isshoni-tui
VIEW ?= dashboard
SIZE ?= 140x40
CHROME ?= $(shell for c in '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' google-chrome google-chrome-stable chromium chromium-browser; do if [ -x "$$c" ]; then echo "$$c"; break; elif p=$$(command -v "$$c"); then echo "$$p"; break; fi; done)
SHOT := shots/$(VIEW)-$(SIZE)

# Get a fresh clone ready: check the toolchain, fetch the Go modules, build. Installs nothing system-wide.
install:
	@command -v go >/dev/null || { echo "isshoni: Go is missing. Install Go 1.27 or newer (https://go.dev/dl/ or your package manager), then run make install again." >&2; exit 1; }
	@command -v python3 >/dev/null || { echo "isshoni: python3 is missing. Install Python 3.11 or newer (https://www.python.org/downloads/ or your package manager), then run make install again." >&2; exit 1; }
	@python3 -c 'import sys; sys.exit(sys.version_info < (3, 11))' || { echo "isshoni: Python 3.11 or newer is required. Install a newer one (https://www.python.org/downloads/ or your package manager)." >&2; exit 1; }
	go mod download
	$(MAKE) build
	@if [ -n "$(CHROME)" ]; then echo "Chrome found: make screenshot is available."; else echo "Chrome or Chromium not found (optional): make screenshot needs one; install it or set CHROME=/path/to/chrome."; fi
	@echo "Ready: run make up, or $(BIN)."

# Build the dashboard and run it.
up: build
	$(BIN)

build:
	go build -o $(BIN) ./cmd/isshoni-tui

test: test-factory
	python3 -m unittest discover -s tests
	go vet ./...
	go test ./...

test-factory:
	python3 -m unittest discover -s factory/tests

# Render one frame to stdout without a TTY, e.g. make snapshot VIEW=session ARGS="--session 04ca0148 --no-color"
snapshot: build
	$(BIN) --snapshot $(VIEW) --size $(SIZE) $(ARGS)

# Render one colored frame to shots/<view>-<size>.png (needs Chrome or Chromium; set CHROME=... to pick one), e.g. make screenshot VIEW=session ARGS="--session 04ca0148"
screenshot: build
	@[ -n "$(CHROME)" ] || { echo "isshoni: Chrome or Chromium is missing. Install one, or set CHROME=/path/to/chrome." >&2; exit 1; }
	@mkdir -p shots
	$(BIN) --snapshot $(VIEW) --size $(SIZE) $(ARGS) | python3 tools/ansi2html.py > $(SHOT).html
	"$(CHROME)" --headless --disable-gpu --hide-scrollbars \
		--window-size=$$(( $(firstword $(subst x, ,$(SIZE))) * 9 + 16 )),$$(( $(lastword $(subst x, ,$(SIZE))) * 17 + 16 )) \
		--screenshot="$(CURDIR)/$(SHOT).png" "file://$(CURDIR)/$(SHOT).html" >/dev/null 2>&1
	@echo $(SHOT).png

clean:
	rm -rf $(BIN) shots
