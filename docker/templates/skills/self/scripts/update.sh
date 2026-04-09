#!/usr/bin/env bash
# Delegates to the agent's own update script
set -euo pipefail

AGENT="${AGENT_NAME:-agent}"
AGENT_DIR="/root/.config/term-llm/agents/$AGENT"

bash "$AGENT_DIR/scripts/update.sh"
