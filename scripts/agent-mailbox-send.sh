#!/usr/bin/env bash
set -euo pipefail

# Script to dispatch an asynchronous task message to a remote Herd agent's mailbox
# and wait for the remote agent to wake up, execute the task, and publish a symmetric reply.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

HERD_BIN="$ROOT_DIR/herd"
if [ ! -x "$HERD_BIN" ]; then
    HERD_BIN="herd"
fi

TARGET_NODE="${1:-}"
MESSAGE="${2:-}"
TOPIC="${3:-task_request}"
TIMEOUT_SECS="${4:-60}"
LOCAL_NODE="${5:-node-1}"

if [ -z "$TARGET_NODE" ] || [ -z "$MESSAGE" ]; then
    echo "Usage: $0 <TARGET_NODE> \"<MESSAGE>\" [TOPIC] [TIMEOUT_SECS] [LOCAL_NODE]" >&2
    echo "Example: $0 node-2 \"Check system status\" system_check 60 node-1" >&2
    exit 1
fi

echo "Depositing task into remote mailbox..."
echo " - Sender:    $LOCAL_NODE"
echo " - Target:    $TARGET_NODE"
echo " - Topic:     $TOPIC"
echo " - Message:   $MESSAGE"

"$HERD_BIN" mail send --node-name "$LOCAL_NODE" --topic "$TOPIC" --wait --timeout "${TIMEOUT_SECS}s" "$TARGET_NODE" "$MESSAGE"
