BINARY_NAME=dns-server
VERSION=1.0.0
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-ldflags "-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME) -s -w"

# Build for Linux (production target)
.PHONY: build
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o bin/$(BINARY_NAME) ./cmd/dns-server

# Build for current OS (development)
.PHONY: build-dev
build-dev:
	go build $(LDFLAGS) -o bin/$(BINARY_NAME) ./cmd/dns-server

# Run locally
.PHONY: run
run:
	go run ./cmd/dns-server -config config.yaml -log-level debug -query-log

# Run tests
.PHONY: test
test:
	go test -v -race ./...

# Run benchmarks
.PHONY: bench
bench:
	go test -bench=. -benchmem ./...

# Clean build artifacts
.PHONY: clean
clean:
	rm -rf bin/

# Install on Linux server
.PHONY: install
install: build
	sudo cp bin/$(BINARY_NAME) /usr/local/bin/
	sudo mkdir -p /etc/dns-server
	sudo cp config.yaml /etc/dns-server/
	sudo useradd -r -s /bin/false dns-server 2>/dev/null || true
	sudo cp dns-server.service /etc/systemd/system/
	sudo systemctl daemon-reload
	sudo systemctl enable dns-server
	@echo "Installation complete. Start with: sudo systemctl start dns-server"

# Uninstall
.PHONY: uninstall
uninstall:
	sudo systemctl stop dns-server 2>/dev/null || true
	sudo systemctl disable dns-server 2>/dev/null || true
	sudo rm -f /etc/systemd/system/dns-server.service
	sudo rm -f /usr/local/bin/$(BINARY_NAME)
	sudo rm -rf /etc/dns-server
	sudo userdel dns-server 2>/dev/null || true
	sudo systemctl daemon-reload

# Cross-compile for ARM (if needed for ARM server)
.PHONY: build-arm
build-arm:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o bin/$(BINARY_NAME)-arm64 ./cmd/dns-server

# Format code
.PHONY: fmt
fmt:
	go fmt ./...

# Lint
.PHONY: lint
lint:
	golangci-lint run ./...
