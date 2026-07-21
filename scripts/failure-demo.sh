#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

if docker compose version >/dev/null 2>&1; then
  COMPOSE="docker compose"
else
  COMPOSE="docker-compose"
fi

RESULTS_DIR="bench/results"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
LOG="$RESULTS_DIR/failure-demo-$STAMP.txt"
mkdir -p "$RESULTS_DIR"

exec > >(tee "$LOG") 2>&1

now() { date -u +%H:%M:%S.%3N; }

wait_for() {
  local name="$1" url="$2" tries=60
  echo -n "waiting for $name..."
  for _ in $(seq 1 "$tries"); do
    if curl -sf -o /dev/null "$url"; then
      echo " up"
      return 0
    fi
    sleep 1
  done
  echo " TIMEOUT"
  exit 1
}

backend_state() {
  curl -sf http://localhost:9090/backends \
    | jq -r --arg addr "$1" '.backends[] | select(.address == $addr) | "\(.healthy) \(.circuit_state // "n/a")"'
}

echo "--- bringing up main stack (skips already-running services) ---"
$COMPOSE up -d --build

wait_for "aegis control-plane" "http://localhost:9090/health"
wait_for "aegis data-plane"    "http://localhost:8080/api/test"

echo
echo "--- baseline: 12 requests through :8080/api/test ---"
declare -A baseline_counts
for i in $(seq 1 12); do
  server=$(curl -sf http://localhost:8080/api/test | jq -r .server)
  baseline_counts[$server]=$(( ${baseline_counts[$server]:-0} + 1 ))
done
for k in "${!baseline_counts[@]}"; do
  echo "  $k: ${baseline_counts[$k]} requests"
done

echo
echo "--- baseline circuit state ---"
for b in "backend1:3000" "backend2:3001" "backend3:3002"; do
  echo "  $b: $(backend_state "$b")"
done

echo
echo "=== [$(now)] killing backend1 (docker kill, abrupt) ==="
$COMPOSE kill backend1

echo "--- [$(now)] firing burst of 40 requests immediately (no delay) ---"
declare -A burst_counts
burst_errors=0
for i in $(seq 1 40); do
  if server=$(curl -sf --max-time 2 http://localhost:8080/api/test 2>/dev/null | jq -r .server 2>/dev/null) && [ -n "$server" ]; then
    burst_counts[$server]=$(( ${burst_counts[$server]:-0} + 1 ))
  else
    burst_errors=$(( burst_errors + 1 ))
  fi
done
echo "  burst results:"
for k in "${!burst_counts[@]}"; do
  echo "    $k: ${burst_counts[$k]} requests"
done
echo "    failed/errored: $burst_errors"

echo
echo "--- polling backend1 state every 2s for 20s (health check + circuit breaker reacting) ---"
for i in $(seq 1 10); do
  echo "  [$(now)] backend1: $(backend_state "backend1:3000")"
  sleep 2
done

echo
echo "=== [$(now)] restarting backend1 ==="
$COMPOSE start backend1

echo "--- polling backend1 state every 3s for 45s (health check + CB timeout recovery) ---"
for i in $(seq 1 15); do
  echo "  [$(now)] backend1: $(backend_state "backend1:3000")"
  sleep 3
done

echo
echo "--- final: 12 requests, confirm backend1 back in rotation ---"
declare -A final_counts
for i in $(seq 1 12); do
  server=$(curl -sf http://localhost:8080/api/test | jq -r .server)
  final_counts[$server]=$(( ${final_counts[$server]:-0} + 1 ))
done
for k in "${!final_counts[@]}"; do
  echo "  $k: ${final_counts[$k]} requests"
done

echo
echo "=== done, transcript: $LOG ==="
