.PHONY: all proto build-go build-rust run-control run-data test clean

# Variables
PROTO_DIR := proto
GO_OUT := control-plane/proto
RUST_OUT := data-plane/src

all: proto build-go build-rust

# Generate protobuf code
proto:
	@echo "Generating protobuf code..."
	@mkdir -p $(GO_OUT)
	protoc --go_out=$(GO_OUT) --go_opt=paths=source_relative,Mproto/proxy.proto=github.com/lazzerex/aegis/control-plane/proto \
		--go-grpc_out=$(GO_OUT) --go-grpc_opt=paths=source_relative,Mproto/proxy.proto=github.com/lazzerex/aegis/control-plane/proto \
		$(PROTO_DIR)/proxy.proto
	@echo "Moving generated files..."
	@if [ -d "$(GO_OUT)/proto" ]; then mv $(GO_OUT)/proto/*.go $(GO_OUT)/ && rmdir $(GO_OUT)/proto; fi

# Build Go control plane
build-go: proto
	@echo "Building Go control plane..."
	@mkdir -p bin
	cd control-plane && go mod download
	cd control-plane && go build -o aegis-control ./cmd/main.go
	@echo "✓ Control plane binary: control-plane/aegis-control"

# Build Rust data plane
build-rust:
	@echo "Building Rust data plane..."
	cd data-plane && cargo build --release
	@echo "✓ Data plane binary: data-plane/target/release/aegis-data"

# Run control plane
run-control:
	@echo "Starting Aegis control plane..."
	@if [ ! -f "control-plane/aegis-control" ]; then \
		echo "Error: Control plane not built. Run 'make build-go' first."; \
		exit 1; \
	fi
	./control-plane/aegis-control --config config.yaml

# Run data plane
run-data:
	@echo "Starting Aegis data plane..."
	@if [ ! -f "data-plane/target/release/aegis-data" ]; then \
		echo "Error: Data plane not built. Run 'make build-rust' first."; \
		exit 1; \
	fi
	cd data-plane && RUST_LOG=info ./target/release/aegis-data --config ../config.yaml

# Run both (in separate terminals required)
run: build-go build-rust
	@echo "Run the following in separate terminals:"
	@echo "  Terminal 1: make run-data"
	@echo "  Terminal 2: make run-control"

# Test
test:
	@echo "Running tests..."
	cd control-plane && go test ./...
	cd data-plane && cargo test

# Test scripts
test-proxy:
	@./scripts/test-proxy.sh test-proxy

test-advanced:
	@./scripts/test-advanced-features.sh

test-udp:
	@./scripts/test-udp-proxy.sh test

udp-backends-start:
	@./scripts/test-udp-proxy.sh start

udp-backends-stop:
	@./scripts/test-udp-proxy.sh stop

# Start/stop test backends
backends-start:
	@./scripts/test-proxy.sh start

backends-stop:
	@./scripts/test-proxy.sh stop

backends-status:
	@./scripts/test-proxy.sh status

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -f control-plane/aegis-control
	rm -rf control-plane/proto/*.pb.go
	cd control-plane && go clean
	cd data-plane && cargo clean

# Install dependencies
deps:
	@echo "Installing dependencies..."
	@echo "Installing Go dependencies..."
	cd control-plane && go mod download
	@echo "Installing protoc-gen-go..."
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	@echo "Installing Rust dependencies..."
	cd data-plane && cargo fetch

# Docker commands
docker-build:
	@echo "Building Docker images..."
	docker-compose build

docker-up:
	@echo "Starting services with Docker Compose..."
	docker-compose up -d

docker-down:
	@echo "Stopping services..."
	docker-compose down

docker-logs:
	docker-compose logs -f

docker-test:
	@echo "Testing Aegis in Docker..."
	@sleep 5
	@echo "Testing TCP proxy..."
	@curl -s http://localhost:8080/api/test || echo "TCP proxy not ready"
	@echo "\n=== Testing UDP proxy ==="
	@echo "Sending test packets to UDP proxy..."
	@for i in 1 2 3 4 5; do \
		echo "test_packet_$$i" | nc -u -w1 localhost 8081 2>/dev/null || echo "UDP test $$i: no response"; \
		sleep 0.5; \
	done
	@echo "\n=== Checking health ==="
	@curl -s http://localhost:9090/health | jq . || echo "Health check not ready"
	@echo "\n=== Checking metrics ==="
	@curl -s http://localhost:9091/metrics | grep -E "(proxy|udp|tcp|rate_limit|circuit)" | head -10 || echo "Metrics not ready"

docker-restart: docker-down docker-up

# Format code
fmt:
	cd control-plane && go fmt ./...
	cd data-plane && cargo fmt

# Lint
lint:
	cd control-plane && go vet ./...
	cd data-plane && cargo clippy

# Development workflow
dev: fmt lint test build-go build-rust
