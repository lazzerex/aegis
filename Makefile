.PHONY: all build build-go build-rust build-tui proto \
        test test-unit test-integration test-proxy test-advanced test-udp \
        run run-control run-data run-tui \
        backends-start backends-stop backends-status \
        udp-backends-start udp-backends-stop \
        check fmt lint \
        docker-build docker-up docker-down docker-logs docker-test docker-restart \
        deps clean

# Variables
PROTO_DIR    := proto
GO_OUT       := control-plane/proto
CONTROL_BIN  := control-plane/aegis-control
CTL_BIN      := control-plane/aegis-ctl
TUI_BIN      := control-plane/aegis-tui
DATA_BIN     := data-plane/target/release/aegis-data

# ── Top-level aliases ─────────────────────────────────────────────────────────

all: build

build: build-go build-rust

# ── Protobuf ─────────────────────────────────────────────────────────────────

proto:
	@mkdir -p $(GO_OUT)
	protoc \
		--go_out=$(GO_OUT) \
		--go_opt=paths=source_relative,Mproto/proxy.proto=github.com/lazzerex/aegis/control-plane/proto \
		--go-grpc_out=$(GO_OUT) \
		--go-grpc_opt=paths=source_relative,Mproto/proxy.proto=github.com/lazzerex/aegis/control-plane/proto \
		$(PROTO_DIR)/proxy.proto
	@if [ -d "$(GO_OUT)/proto" ]; then mv $(GO_OUT)/proto/*.go $(GO_OUT)/ && rmdir $(GO_OUT)/proto; fi

# ── Build ─────────────────────────────────────────────────────────────────────

build-go: proto
	cd control-plane && go mod tidy && go build -o aegis-control ./cmd/main.go
	cd control-plane && go build -o aegis-ctl ./cmd/aegis-ctl/main.go
	@echo "control plane: $(CONTROL_BIN)"
	@echo "ctl:           $(CTL_BIN)"

build-rust:
	cd data-plane && cargo build --release
	@echo "data plane:    $(DATA_BIN)"

build-tui:
	cd control-plane && go build -o aegis-tui ./cmd/aegis-tui
	@echo "tui:           $(TUI_BIN)"

# ── Test ─────────────────────────────────────────────────────────────────────

# Unit tests only — no running services required
test: test-unit

test-unit: proto
	cd control-plane && go test ./...
	cd data-plane && cargo test

# Integration tests — require running proxy + backends
test-integration: test-proxy test-udp test-advanced

test-proxy:
	./scripts/test-proxy.sh test-proxy

test-udp:
	./scripts/test-udp-proxy.sh test

test-advanced:
	./scripts/test-advanced-features.sh

# ── Run ───────────────────────────────────────────────────────────────────────

run: build
	@echo "Start in two terminals:"
	@echo "  make run-data"
	@echo "  make run-control"

run-data:
	@test -f "$(DATA_BIN)" || { echo "Not built. Run: make build-rust"; exit 1; }
	RUST_LOG=info ./$(DATA_BIN)

run-control:
	@test -f "$(CONTROL_BIN)" || { echo "Not built. Run: make build-go"; exit 1; }
	./$(CONTROL_BIN) --config config.yaml

run-tui:
	@test -f "$(TUI_BIN)" || { echo "Not built. Run: make build-tui"; exit 1; }
	./$(TUI_BIN)

# ── Test backends ─────────────────────────────────────────────────────────────

backends-start:
	./scripts/test-proxy.sh start

backends-stop:
	./scripts/test-proxy.sh stop

backends-status:
	./scripts/test-proxy.sh status

udp-backends-start:
	./scripts/test-udp-proxy.sh start

udp-backends-stop:
	./scripts/test-udp-proxy.sh stop

# ── Code quality ──────────────────────────────────────────────────────────────

# Full quality gate: format + lint + unit tests
check: fmt lint test-unit

fmt:
	cd control-plane && go fmt ./...
	cd data-plane && cargo fmt

lint:
	cd control-plane && go vet ./...
	cd data-plane && cargo clippy

# ── Docker ────────────────────────────────────────────────────────────────────

docker-build:
	docker-compose build

docker-up:
	docker-compose up -d

docker-down:
	docker-compose down

docker-logs:
	docker-compose logs -f

docker-restart: docker-down docker-up

docker-test:
	@sleep 5
	@curl -s http://localhost:8080/api/test || echo "TCP proxy not ready"
	@for i in 1 2 3 4 5; do \
		echo "test_packet_$$i" | nc -u -w1 localhost 8081 2>/dev/null || echo "UDP $$i: no response"; \
		sleep 0.5; \
	done
	@curl -s http://localhost:9090/health | jq . || echo "Health check not ready"
	@curl -s http://localhost:9091/metrics | grep -E "(proxy|udp|tcp|rate_limit|circuit)" | head -10 || echo "Metrics not ready"

# ── Setup / clean ─────────────────────────────────────────────────────────────

deps:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.0
	cd control-plane && go mod tidy
	cd control-plane/proto && go mod tidy
	cd data-plane && cargo fetch

clean:
	rm -f $(CONTROL_BIN)
	rm -f $(GO_OUT)/*.pb.go
	cd control-plane && go clean
	cd data-plane && cargo clean
