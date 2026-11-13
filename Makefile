.PHONY: up build test snapshot screenshot clean

BIN := bin/ccmon-tui
VIEW ?= dashboard
SIZE ?= 140x40
CHROME ?= /Applications/Google Chrome.app/Contents/MacOS/Google Chrome
SHOT := shots/$(VIEW)-$(SIZE)

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
