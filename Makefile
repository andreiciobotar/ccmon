.PHONY: install up build test snapshot screenshot clean

BIN := bin/ccmon-tui
VIEW ?= dashboard
SIZE ?= 140x40
CHROME ?= /Applications/Google Chrome.app/Contents/MacOS/Google Chrome
SHOT := shots/$(VIEW)-$(SIZE)

# Get a fresh clone ready: check the toolchain, fetch the Go modules, build. Installs nothing system-wide.
install:
	@command -v go >/dev/null || { echo "ccmon: Go is missing. Install Go 1.27 or newer (https://go.dev/dl/ or: brew install go), then run make install again." >&2; exit 1; }
	@command -v python3 >/dev/null || { echo "ccmon: python3 is missing. Install Python 3.11 or newer (https://www.python.org/downloads/ or: brew install python), then run make install again." >&2; exit 1; }
	@python3 -c 'import sys; sys.exit(sys.version_info < (3, 11))' || { echo "ccmon: Python 3.11 or newer is required. Install a newer one (https://www.python.org/downloads/ or: brew install python)." >&2; exit 1; }
	go mod download
	$(MAKE) build
	@if [ -x "$(CHROME)" ]; then echo "Chrome found: make screenshot is available."; else echo "Chrome not found (optional): make screenshot needs it; set CHROME=/path/to/chrome."; fi
	@echo "Ready: run make up, or $(BIN)."

# Build the dashboard and run it.
up: build
	$(BIN)

build:
	go build -o $(BIN) ./cmd/ccmon-tui

test:
	python3 -m unittest discover -s tests
	go vet ./...
	go test ./...

# Render one frame to stdout without a TTY, e.g. make snapshot VIEW=session ARGS="--session 04ca0148 --no-color"
snapshot: build
	$(BIN) --snapshot $(VIEW) --size $(SIZE) $(ARGS)

# Render one colored frame to shots/<view>-<size>.png (needs Chrome; set CHROME=... elsewhere), e.g. make screenshot VIEW=session ARGS="--session 04ca0148"
screenshot: build
	@mkdir -p shots
	$(BIN) --snapshot $(VIEW) --size $(SIZE) $(ARGS) | python3 tools/ansi2html.py > $(SHOT).html
	"$(CHROME)" --headless --disable-gpu --hide-scrollbars \
		--window-size=$$(( $(firstword $(subst x, ,$(SIZE))) * 9 + 16 )),$$(( $(lastword $(subst x, ,$(SIZE))) * 17 + 16 )) \
		--screenshot="$(CURDIR)/$(SHOT).png" "file://$(CURDIR)/$(SHOT).html" >/dev/null 2>&1
	@echo $(SHOT).png

clean:
	rm -rf $(BIN) shots
