#!/usr/bin/env bash
set -euo pipefail

# Helper script for running local multi-node Herd test clusters

BASE_DIR="${HERD_TEST_DIR:-/tmp/herd-test-cluster}"
BIN="./bin/herd"

mkdir -p "$BASE_DIR"

build_binary() {
    if [ ! -f "$BIN" ]; then
        echo "Building herd binary..."
        make build
    fi
}

start_cluster() {
    local count="${1:-3}"
    build_binary
    echo "Starting $count-node local cluster in $BASE_DIR..."

    # Node 1 (Seed)
    export HERD_NODE_NAME="node-1"
    export HERD_DATA_DIR="$BASE_DIR/node-1/data"
    export HERD_CONFIG_DIR="$BASE_DIR/node-1/config"
    export HERD_STATE_DIR="$BASE_DIR/node-1/state"
    export HERD_SOCKET="$BASE_DIR/node-1/herd.sock"
    export HERD_BIND_PORT="7946"

    $BIN daemon > "$BASE_DIR/node-1.log" 2>&1 &
    local p1=$!
    echo "$p1" > "$BASE_DIR/node-1.pid"
    echo "Started node-1 (PID: $p1, Port: 7946)"

    sleep 1

    # Additional Nodes
    for i in $(seq 2 "$count"); do
        local name="node-$i"
        local port=$((7946 + i - 1))
        export HERD_NODE_NAME="$name"
        export HERD_DATA_DIR="$BASE_DIR/$name/data"
        export HERD_CONFIG_DIR="$BASE_DIR/$name/config"
        export HERD_STATE_DIR="$BASE_DIR/$name/state"
        export HERD_SOCKET="$BASE_DIR/$name/herd.sock"
        export HERD_BIND_PORT="$port"
        export HERD_JOIN="127.0.0.1:7946"

        $BIN daemon > "$BASE_DIR/$name.log" 2>&1 &
        local pid=$!
        echo "$pid" > "$BASE_DIR/$name.pid"
        echo "Started $name (PID: $pid, Port: $port, Joining 127.0.0.1:7946)"
    done

    echo "Cluster started. Run '$0 status' to inspect."
}

status_cluster() {
    for pidfile in "$BASE_DIR"/*.pid; do
        if [ -f "$pidfile" ]; then
            local name
            name=$(basename "$pidfile" .pid)
            local sock="$BASE_DIR/$name/herd.sock"
            echo "--- Status for $name ---"
            if [ -S "$sock" ]; then
                $BIN status --socket "$sock" || true
                echo ""
                $BIN roster --socket "$sock" || true
            else
                echo "Socket not found for $name"
            fi
            echo ""
        fi
    done
}

stop_cluster() {
    echo "Stopping cluster nodes..."
    for pidfile in "$BASE_DIR"/*.pid; do
        if [ -f "$pidfile" ]; then
            local pid
            pid=$(cat "$pidfile")
            kill "$pid" 2>/dev/null || true
            rm -f "$pidfile"
        fi
    done
    rm -rf "$BASE_DIR"
    echo "Cluster stopped and cleaned up."
}

case "${1:-}" in
    start)
        start_cluster "${2:-3}"
        ;;
    status)
        status_cluster
        ;;
    stop)
        stop_cluster
        ;;
    *)
        echo "Usage: $0 {start [count]|status|stop}"
        exit 1
        ;;
esac
