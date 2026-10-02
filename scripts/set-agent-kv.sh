#!/usr/bin/env bash
set -euo pipefail

# Script to configure the Herd AI agent via the distributed KV store.
# Changes are automatically gossiped to all cluster nodes.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# 1. Resolve API Key
API_KEY="${1:-}"
if [ -z "$API_KEY" ]; then
    if [ -f "$ROOT_DIR/.env" ]; then
        API_KEY=$(grep -E '^GEMINI_API_KEY=' "$ROOT_DIR/.env" | cut -d'=' -f2- | tr -d '"'\'' ')
    fi
fi

if [ -z "$API_KEY" ]; then
    echo "Error: GEMINI_API_KEY not found in argument or .env" >&2
    echo "Usage: $0 [API_KEY] [MODEL] [NODE_NAME]" >&2
    exit 1
fi

# 2. Resolve Model and Node Name
MODEL="${2:-gemini-3.8-flash}"
NODE_NAME="${3:-node-1}"

echo "Configuring Herd Agent in Distributed KV..."
echo " - Provider:  gemini"
echo " - Model:     $MODEL"
echo " - Key:       ${API_KEY:0:10}...${API_KEY: -6}"
echo " - Node:      $NODE_NAME"

# 3. Construct JSON Payload
CONFIG_JSON=$(python3 -c "
import json, sys
data = {
    'provider': 'gemini',
    'model': sys.argv[1],
    'api_key': sys.argv[2]
}
print(json.dumps(data))
" "$MODEL" "$API_KEY")

# 4. Set in Distributed KV Store
HERD_BIN="$ROOT_DIR/bin/herd"
if [ ! -x "$HERD_BIN" ]; then
    HERD_BIN="$ROOT_DIR/herd"
    if [ ! -x "$HERD_BIN" ]; then
        HERD_BIN="herd"
    fi
fi

SOCKET_ARGS=()
if [ -n "${HERD_SOCKET:-}" ]; then
    SOCKET_ARGS=("-socket" "$HERD_SOCKET")
else
    SOCKET_ARGS=("-node-name" "$NODE_NAME")
fi

"$HERD_BIN" kv set "${SOCKET_ARGS[@]}" agent:config:global "$CONFIG_JSON"

echo "agent:config:global successfully written to KV."

# 5. Verify Propagation
echo "Verifying active configuration from KV:"
"$HERD_BIN" kv get "${SOCKET_ARGS[@]}" agent:config:global
echo ""
echo "Configuration is now replicated across all mesh nodes via LWW gossip."
