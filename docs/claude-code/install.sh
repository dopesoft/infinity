#!/usr/bin/env bash
# Idempotent installer for the home-Mac Claude Code bridge.
# See docs/claude-code/SETUP.md for the full runbook.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
LAUNCH_DIR="$HOME/Library/LaunchAgents"
LOG_DIR="$HOME/Library/Logs/infinity-coder"
ENV_FILE="$HOME/.config/infinity/coder.env"

say() { printf "▸ %s\n" "$*"; }

# 1. Tooling
if ! command -v cloudflared >/dev/null 2>&1; then
  say "installing cloudflared"
  brew install cloudflared
fi
if ! command -v mcp-proxy >/dev/null 2>&1; then
  say "installing mcp-proxy"
  npm install -g mcp-proxy
fi
if ! command -v claude >/dev/null 2>&1; then
  say "claude CLI not found — install Claude Code first (https://docs.anthropic.com/claude-code) and run 'claude /login'"
  exit 1
fi

# 2. Bearer token (32 random bytes, base64) — generated once, persisted.
mkdir -p "$(dirname "$ENV_FILE")"
chmod 700 "$(dirname "$ENV_FILE")"
if [ ! -f "$ENV_FILE" ] || ! grep -q '^CLAUDE_CODE_TUNNEL_SECRET=' "$ENV_FILE"; then
  say "generating bearer token at $ENV_FILE"
  TOKEN=$(openssl rand -base64 32 | tr -d '=+/' | head -c 48)
  cat > "$ENV_FILE" <<EOF
# Generated $(date -u +%FT%TZ) by docs/claude-code/install.sh
# Paste this exact value into Railway core env as CLAUDE_CODE_TUNNEL_SECRET
# AND into the Cloudflare Access "Service Auth" policy header value.
CLAUDE_CODE_TUNNEL_SECRET=$TOKEN
EOF
  chmod 600 "$ENV_FILE"
else
  say "reusing existing bearer token at $ENV_FILE"
fi

# 3. Log dir
mkdir -p "$LOG_DIR"
mkdir -p "$LAUNCH_DIR"

# 4. Substitute $HOME into plist templates and copy.
PLIST_SRC="$REPO_ROOT/docs/claude-code/launchd"
for name in claude-mcp mcp-proxy cloudflared caffeinate; do
  plist="dev.dopesoft.$name.plist"
  src="$PLIST_SRC/$plist"
  dst="$LAUNCH_DIR/$plist"
  if [ ! -f "$src" ]; then
    say "WARN: missing $src"
    continue
  fi
  say "installing $plist"
  sed "s|__HOME__|$HOME|g" "$src" > "$dst"
  chmod 644 "$dst"
  # Reload safely.
  launchctl bootout "gui/$UID/dev.dopesoft.$name" 2>/dev/null || true
  launchctl bootstrap "gui/$UID" "$dst"
done

say "done. Verify: launchctl list | grep dopesoft"
say "logs: $LOG_DIR"
say "next: paste the bearer into Railway core env + Cloudflare Access policy."
