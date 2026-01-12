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
	cd control-plane && go mod download
	cd control-plane && go build -o ../bin/aegis-control ./cmd/main.go

# Build Rust data plane
build-rust:
	@echo "Building Rust data plane..."
	cd data-plane && cargo build --release
	cp data-plane/target/release/aegis-data bin/

# Run control plane
run-control: build-go
	@echo "Starting Aegis control plane..."
	./bin/aegis-control --config config.yaml

# Run data plane
run-data: build-rust
	@echo "Starting Aegis data plane..."
	RUST_LOG=info ./bin/aegis-data

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
	rm -rf bin/
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

# Create bin directory
init:
	mkdir -p bin
