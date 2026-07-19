#!/usr/bin/env bash
# Aegis proxy overhead benchmark.
#
# Measures throughput/latency hitting nginx directly vs. through the Aegis
# TCP proxy, using the same nginx backend for both arms so the delta is
# Aegis's added overhead, not backend variance.
#
# Requires: docker (for wrk, via the williamyeh/wrk image — no local
# install needed).
#
# Usage: ./bench/run-bench.sh [duration] [connections] [threads]
set -euo pipefail

cd "$(dirname "$0")/.."

DURATION="${1:-15s}"
CONNS="${2:-100}"
THREADS="${3:-4}"

if docker compose version >/dev/null 2>&1; then
  COMPOSE="docker compose -f bench/docker-compose.bench.yml -p aegis-bench"
else
  COMPOSE="docker-compose -f bench/docker-compose.bench.yml -p aegis-bench"
fi
RESULTS_DIR="bench/results"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"

mkdir -p "$RESULTS_DIR"

cleanup() {
  echo "--- tearing down bench stack ---"
  $COMPOSE down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "--- building + starting bench stack ---"
$COMPOSE up -d --build

NET="aegis-bench_default"

wait_for() {
  local name="$1" url="$2" tries=60
  echo -n "waiting for $name..."
  for _ in $(seq 1 $tries); do
    if curl -sf -o /dev/null "$url"; then
      echo " up"
      return 0
    fi
    sleep 1
  done
  echo " TIMEOUT"
  echo "--- last 40 lines of compose logs ---"
  $COMPOSE logs --tail=40
  exit 1
}

wait_for "nginx (direct)"       "http://localhost:18081/"
wait_for "aegis control-plane"  "http://localhost:19090/health"
wait_for "aegis data-plane"     "http://localhost:18080/"

run_wrk() {
  local label="$1" target="$2" out="$3"
  echo "--- running: $label ($DURATION, $CONNS conns, $THREADS threads) ---"
  docker run --rm --network "$NET" williamyeh/wrk \
    -t"$THREADS" -c"$CONNS" -d"$DURATION" --latency "$target" | tee "$out"
}

# Warmup (pre-warmed connection pool, JIT/branch caches, nginx workers)
echo "--- warmup ---"
docker run --rm --network "$NET" williamyeh/wrk \
  -t2 -c20 -d5s http://nginx/ >/dev/null

run_wrk "direct -> nginx"        "http://nginx/"        "$RESULTS_DIR/direct-$STAMP.txt"
run_wrk "through Aegis -> nginx" "http://data-plane:8080/" "$RESULTS_DIR/aegis-$STAMP.txt"

$COMPOSE logs --tail=200 > "$RESULTS_DIR/compose-logs-$STAMP.txt" 2>&1 || true

echo
echo "=== Summary ==="
echo "direct:  $RESULTS_DIR/direct-$STAMP.txt"
echo "aegis:   $RESULTS_DIR/aegis-$STAMP.txt"
echo
grep -E "Requests/sec|Latency|50%|90%|99%" "$RESULTS_DIR/direct-$STAMP.txt" | sed 's/^/[direct] /'
echo
grep -E "Requests/sec|Latency|50%|90%|99%" "$RESULTS_DIR/aegis-$STAMP.txt" | sed 's/^/[aegis]  /'
