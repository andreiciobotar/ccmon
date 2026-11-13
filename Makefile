.PHONY: up build test snapshot clean

BIN := bin/ccmon-tui
VIEW ?= dashboard
SIZE ?= 140x40

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

clean:
	rm -f $(BIN)
