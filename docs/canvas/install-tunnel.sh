#!/usr/bin/env bash
# install-tunnel.sh — add a preview.dopesoft.io ingress to the local
# cloudflared config and reload the launchd job. Idempotent; dry-run by
# default. Pass --apply to commit changes.

set -euo pipefail

HOSTNAME="${PREVIEW_HOSTNAME:-preview.dopesoft.io}"
TARGET="${PREVIEW_TARGET:-http://127.0.0.1:3000}"

CONFIG_CANDIDATES=(
  "$HOME/.cloudflared/config.yml"
  "$HOME/.cloudflared/config.yaml"
  "/etc/cloudflared/config.yml"
)

APPLY=0
for arg in "$@"; do
  case "$arg" in
    --apply) APPLY=1 ;;
    --hostname) shift; HOSTNAME="$1" ;;
    --target)   shift; TARGET="$1" ;;
    -h|--help)
      cat <<EOF
Usage: $0 [--apply] [--hostname HOST] [--target URL]

Adds an ingress rule to your local cloudflared config and reloads launchd.
Dry-run by default; pass --apply to write changes.

Env:
  PREVIEW_HOSTNAME  override hostname (default: preview.dopesoft.io)
  PREVIEW_TARGET    override target  (default: http://127.0.0.1:3000)
EOF
      exit 0 ;;
  esac
done

config=""
for c in "${CONFIG_CANDIDATES[@]}"; do
  if [[ -f "$c" ]]; then config="$c"; break; fi
done
if [[ -z "$config" ]]; then
  echo "fatal: no cloudflared config.yml found in known locations" >&2
  echo "       expected one of:" >&2
  for c in "${CONFIG_CANDIDATES[@]}"; do echo "         $c" >&2; done
  exit 2
fi

echo "config: $config"
echo "hostname: $HOSTNAME"
echo "target:   $TARGET"
echo

if grep -q "hostname: $HOSTNAME" "$config"; then
  echo "skip: $HOSTNAME already present in ingress."
  if (( APPLY )); then echo "nothing to write."; fi
  exit 0
fi

# Insert above the catch-all 404 line, or append if there isn't one.
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

awk -v host="$HOSTNAME" -v target="$TARGET" '
  /service: http_status:404/ && !done {
    print "  - hostname: " host
    print "    service: " target
    done = 1
  }
  { print }
  END {
    if (!done) {
      print "  - hostname: " host
      print "    service: " target
    }
  }
' "$config" > "$tmp"

echo "--- diff ---"
diff -u "$config" "$tmp" || true
echo "--- end ---"
echo

if (( ! APPLY )); then
  echo "dry-run; pass --apply to write."
  exit 0
fi

cp "$config" "${config}.bak.$(date +%Y%m%d%H%M%S)"
mv "$tmp" "$config"
trap - EXIT
echo "wrote $config"

# Reload via launchd if installed.
plist="$HOME/Library/LaunchAgents/com.cloudflare.cloudflared.plist"
if [[ -f "$plist" ]]; then
  launchctl unload "$plist" 2>/dev/null || true
  launchctl load   "$plist"
  echo "reloaded $plist"
fi

echo "done. configure DNS:"
echo "  cloudflared tunnel route dns <tunnel-id> $HOSTNAME"
