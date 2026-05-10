# Claude Code on the Mac → Infinity (ToS-clean)

This is the runbook for the home-Mac coder bridge. End state: phone (or anywhere)
asks Infinity to code; Railway Core invokes the `claude_code` MCP tool; the call
hops through Cloudflare Tunnel to your Mac where `claude mcp serve` runs under
your **Max subscription** (no API charges). All actions get captured into
Infinity's memory and gated through the Trust queue.

## Compliance note

Anthropic's Feb 2026 Consumer Terms restrict OAuth tokens to **Claude Code itself
and claude.ai**. This setup never moves tokens off the Mac. Infinity orchestrates
the `claude` CLI via the official `mcp serve` mode — the supported way to use
Claude Code as a tool. Token storage (macOS Keychain) is untouched.

If you ever rip the OAuth credentials out of `~/.claude/.credentials.json` and use
them from another machine, that's the violation. Don't.

---

## Pieces

| Component | Path | Purpose |
|---|---|---|
| `claude mcp serve` | bundled with Claude Code | exposes Bash/Read/Write/Edit/Grep/Glob via stdio MCP |
| `mcp-proxy` | npm | bridges stdio → SSE so the tunnel can carry it |
| `cloudflared` | brew | tunnels SSE to a public hostname behind Cloudflare Access |
| launchd LaunchAgents | `~/Library/LaunchAgents/` | autostart on login, restart on crash |
| `caffeinate -dimsu` | system | keeps Mac awake when plugged in |
| `core/config/mcp.yaml` | this repo | registers `claude_code` server (already added; flip `enabled: true`) |

## Prereqs

- macOS 14+, on a desk Mac (or Mac mini) plugged in 24/7.
- Logged in to your `claude` CLI with a **Max** plan: `claude /login` interactive.
  Verify: `claude /status` shows `oauth subscription` and a non-empty
  `CLAUDE_CODE_OAUTH_TOKEN` line.
- Homebrew + Node 20+ installed.
- A Cloudflare account with a domain you control (e.g. `dopesoft.dev`).

---

## 0. One-time install

```sh
# This script is idempotent. Re-run after upgrades.
bash docs/claude-code/install.sh
```

It does:

1. `brew install cloudflared`
2. `npm install -g mcp-proxy`
3. Generates a 32-byte random bearer token in `~/.config/infinity/coder.env`
4. Writes the three LaunchAgent plists to `~/Library/LaunchAgents/`
5. Loads them with `launchctl bootstrap gui/$UID`

After that, the three daemons are running. Keep them.

## 1. Cloudflare Tunnel

One-time:

```sh
cloudflared login                                # browser OAuth — pick the right zone
cloudflared tunnel create infinity-coder
cloudflared tunnel route dns infinity-coder coder.dopesoft.dev
```

Edit `~/.cloudflared/config.yml`:

```yaml
tunnel: infinity-coder
credentials-file: /Users/n0m4d/.cloudflared/<UUID>.json

ingress:
  - hostname: coder.dopesoft.dev
    service: http://127.0.0.1:8765
    originRequest:
      noTLSVerify: false
      # Token check is enforced at the edge (Access policy). cloudflared
      # forwards Authorization: Bearer <CLAUDE_CODE_TUNNEL_SECRET> straight
      # through to mcp-proxy, which doesn't re-verify it (Access already did).
  - service: http_status:404
```

Reload:

```sh
launchctl kickstart -k gui/$UID/dev.dopesoft.cloudflared
```

## 2. Cloudflare Access policy (Zero Trust)

In the Cloudflare dashboard → Zero Trust → Access → Applications → Add:

- **Type**: Self-hosted
- **App domain**: `coder.dopesoft.dev`
- **Identity providers**: Google Workspace (or whatever you use)
- **Policy 1**: Allow if `email == khaya@malabieindustries.com`
- **Policy 2 (Service Auth)**: Allow if header `Authorization` equals
  `Bearer ${CLAUDE_CODE_TUNNEL_SECRET}` — paste the token from
  `~/.config/infinity/coder.env`. This is what Railway Core uses.

Two policies because: humans browsing get IDP auth; Railway core sends a static
bearer. Both routes hit the same backend.

## 3. Set Railway env vars

```sh
railway variables --service core \
  --set CLAUDE_CODE_TUNNEL_URL=https://coder.dopesoft.dev/sse \
  --set CLAUDE_CODE_TUNNEL_SECRET="$(grep TOKEN ~/.config/infinity/coder.env | cut -d= -f2)"
```

Then in `core/config/mcp.yaml` flip `claude_code.enabled: true` and redeploy
core. On boot you should see:

```
mcp: claude_code connected (7 tools)
```

## 4. Verify end-to-end

From your laptop (away from the home Mac):

```sh
# Direct hit, with the bearer:
curl -N -H "Authorization: Bearer $TOKEN" https://coder.dopesoft.dev/sse | head -5
# Expect: text/event-stream chatter from mcp-proxy.
```

Then in Studio:

> ask "list the files in ~/Dev/infinity"

The agent should call `claude_code__bash` (read-only — auto-allowed by the
default `INFINITY_CLAUDE_CODE_BLOCK=bash,write,edit` policy → wait, bash IS in
the block list). It will queue a Trust contract; check the Trust tab on iOS,
approve, the call fires.

For genuinely read-only ops (grep, ls, glob) the gate auto-allows.

## 5. Trust gating policy

Configured via env on **core** (not the Mac):

| Var | Default | Meaning |
|---|---|---|
| `INFINITY_CLAUDE_CODE_BLOCK` | `bash,write,edit` | tool suffixes that always queue |
| `INFINITY_CLAUDE_CODE_AUTOAPPROVE` | _empty_ | tool suffixes that always allow even if in BLOCK |

Read tools (`read`, `ls`, `grep`, `glob`) are not in the default block list, so
they pass through. If you want zero gating (YOLO mode while iterating):

```sh
railway variables --service core \
  --set INFINITY_CLAUDE_CODE_AUTOAPPROVE=bash,write,edit,read,ls,grep,glob
```

Don't ship that to prod.

## 6. Dealing with `claude` permission prompts

`claude mcp serve` defaults to interactively prompting for risky tool calls.
Since nobody can answer those prompts remotely, run with
`--dangerously-skip-permissions` — Infinity's gate is the actual approval.

The plist (`dev.dopesoft.claude-mcp.plist`) sets that flag. Don't strip it.

## 7. Failure modes & recovery

| Symptom | Cause | Fix |
|---|---|---|
| `mcp: claude_code failed: dial: timeout` | Mac asleep / Wi-Fi hiccup | `caffeinate` daemon should hold it awake; verify with `pmset -g`. cloudflared auto-reconnects. |
| `mcp: claude_code failed: 401` | Bearer mismatch | Re-paste from `~/.config/infinity/coder.env` to Railway env vars. |
| `claude /status` shows expired | OAuth lapsed (rare) | `claude /login` once interactively on the Mac. |
| `Tool exited with status 1` | Real Claude Code failure | Check `~/Library/Logs/infinity-coder/claude-mcp.log`. |
| Mac reboot, daemons don't start | LaunchAgent only runs at user login. | Enable auto-login for the user (System Settings → Users & Groups → Auto-login). |

## 8. Working directory

`claude mcp serve` runs in the dir where it was started. The plist sets it to
`$HOME/Dev` so the agent can `cd` into any project under there. To swap dirs
without restarting, the agent issues `claude_code__bash` with `cd /elsewhere`
inline — Claude Code handles cwd internally per call.

## Files in this directory

- `install.sh` — idempotent setup
- `launchd/dev.dopesoft.claude-mcp.plist`
- `launchd/dev.dopesoft.mcp-proxy.plist`
- `launchd/dev.dopesoft.cloudflared.plist`
- `launchd/dev.dopesoft.caffeinate.plist`

Read each plist before loading. They reference $HOME paths via `<string>`
literals — adjust if your home isn't `/Users/n0m4d`.
