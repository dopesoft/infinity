#!/usr/bin/env bash
# Idempotent installer for the home-Mac Claude Code bridge.
# See docs/claude-code/SETUP.md for the full runbook.
#
# Assumes you ALREADY have cloudflared running as a system LaunchDaemon
# pointing at /etc/cloudflared/config.yml (the existing `jarvis-mac` tunnel).
# This script only installs the user-scope agents that complement it:
# claude-mcp, mcp-proxy, caffeinate. cloudflared is NOT touched.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
LAUNCH_DIR="$HOME/Library/LaunchAgents"
LOG_DIR="$HOME/Library/Logs/infinity-coder"
# Pre-Service-Token versions of this script generated a bearer token at the
# path below. With Cloudflare Service Tokens we no longer need a local
# secret — Railway holds CF_ACCESS_CLIENT_ID / CF_ACCESS_CLIENT_SECRET
# directly. Keep the variable so we can rm any stale file.
LEGACY_ENV_FILE="$HOME/.config/infinity/coder.env"

say() { printf "▸ %s\n" "$*"; }
warn() { printf "⚠ %s\n" "$*" >&2; }

# 1. Tooling
if ! command -v cloudflared >/dev/null 2>&1; then
  warn "cloudflared not found — install with 'brew install cloudflared' (we don't manage the daemon, just expect the binary on PATH)."
fi
if ! command -v mcp-proxy >/dev/null 2>&1; then
  say "installing mcp-proxy"
  npm install -g mcp-proxy
fi
if ! command -v claude >/dev/null 2>&1; then
  warn "claude CLI not found — install Claude Code first (https://docs.anthropic.com/claude-code) and run 'claude /login'"
  exit 1
fi

# 2. Clean up legacy bearer-token file from earlier iterations. With
#    Cloudflare Service Tokens the credential lives only in Railway's env
#    and the Cloudflare dashboard; no local secret needed.
if [ -f "$LEGACY_ENV_FILE" ]; then
  say "removing legacy bearer-token file at $LEGACY_ENV_FILE (Service Tokens replace it)"
  rm -f "$LEGACY_ENV_FILE"
fi

# 3. Log dir
mkdir -p "$LOG_DIR"
mkdir -p "$LAUNCH_DIR"

# 4. Make sure no stale duplicate cloudflared LaunchAgent is hanging around
# from earlier iterations of this script. The system daemon
# (/Library/LaunchDaemons/com.cloudflare.cloudflared.plist) handles cloudflared.
if [ -f "$LAUNCH_DIR/dev.dopesoft.cloudflared.plist" ]; then
  say "removing legacy dev.dopesoft.cloudflared LaunchAgent (system daemon is the canonical one)"
  launchctl bootout "gui/$UID/dev.dopesoft.cloudflared" 2>/dev/null || true
  rm -f "$LAUNCH_DIR/dev.dopesoft.cloudflared.plist"
fi

# 5. Resolve npm global bin so the mcp-proxy plist's PATH includes wherever
#    `claude` and `mcp-proxy` actually live. On a default brew Node setup
#    that's /opt/homebrew/lib/node_modules + /opt/homebrew/bin; on this
#    machine npm is configured with prefix=~/.npm-global so the binaries
#    end up in ~/.npm-global/bin and they would otherwise be invisible to
#    the LaunchAgent.
NPM_BIN="$(npm prefix -g 2>/dev/null)/bin"
if [ ! -d "$NPM_BIN" ] || [ ! -x "$NPM_BIN/mcp-proxy" ]; then
  warn "npm bin '$NPM_BIN' missing or has no mcp-proxy — falling back to /opt/homebrew/bin"
  NPM_BIN="/opt/homebrew/bin"
fi
say "npm global bin resolved to $NPM_BIN"

# 6. Substitute __HOME__ + __NPM_BIN__ into plist templates and copy. Three
#    agents: claude-mcp (placeholder for log namespacing), mcp-proxy (the
#    actual bridge that hosts claude mcp serve), caffeinate (keep Mac awake).
PLIST_SRC="$REPO_ROOT/docs/claude-code/launchd"
for name in claude-mcp mcp-proxy caffeinate; do
  plist="dev.dopesoft.$name.plist"
  src="$PLIST_SRC/$plist"
  dst="$LAUNCH_DIR/$plist"
  if [ ! -f "$src" ]; then
    warn "missing $src"
    continue
  fi
  say "installing $plist"
  sed -e "s|__HOME__|$HOME|g" -e "s|__NPM_BIN__|$NPM_BIN|g" "$src" > "$dst"
  chmod 644 "$dst"
  launchctl bootout "gui/$UID/dev.dopesoft.$name" 2>/dev/null || true
  launchctl bootstrap "gui/$UID" "$dst"
done

say "done. Verify: launchctl list | grep dopesoft"
say "logs: $LOG_DIR"
cat <<EOF

next steps (run these by hand):

  1. add the coder ingress to the system cloudflared config:
       sudo cp ~/.cloudflared/config.yml /etc/cloudflared/config.yml
       sudo launchctl kickstart -k system/com.cloudflare.cloudflared

  2. route DNS for coder.dopesoft.io to the existing tunnel:
       cloudflared tunnel route dns 8e5bd68f-e416-42c8-a0f2-0a8fce21d976 coder.dopesoft.io

  3. Cloudflare Access at https://one.dash.cloudflare.com/
       - Access → Applications → Add → Self-hosted, domain coder.dopesoft.io
       - Policy 1: Allow, Include Emails == your-email@dopesoft.io
       - Access → Service Auth → Service Tokens → Create
         (copy CLIENT_ID + CLIENT_SECRET — secret only shown once)
       - Policy 2: Service Auth, Include Service Token == the one you just created

  4. paste into Railway core env:
       CLAUDE_CODE_TUNNEL_URL=https://coder.dopesoft.io/sse
       CF_ACCESS_CLIENT_ID=<client id>.access
       CF_ACCESS_CLIENT_SECRET=<client secret>

  5. flip core/config/mcp.yaml claude_code.enabled to true and redeploy core.
EOF
