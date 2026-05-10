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

| Component | How it runs | Purpose |
|---|---|---|
| `claude mcp serve` | mcp-proxy child process | exposes Bash/Read/Write/Edit/Grep/Glob via stdio MCP |
| `mcp-proxy` | user `LaunchAgent` (this repo) | bridges stdio → SSE on `127.0.0.1:8765` |
| `cloudflared` | **existing** system `LaunchDaemon` | tunnels SSE through Cloudflare to `coder.dopesoft.io` |
| Cloudflare Access | Zero Trust policy | enforces bearer auth at the edge |
| `caffeinate -dimsu` | user `LaunchAgent` | keeps Mac awake when plugged in |
| `core/config/mcp.yaml` | this repo | registers `claude_code` server (already added; flip `enabled: true`) |

We re-use the existing `jarvis-mac` tunnel (UUID `8e5bd68f-e416-...`) — the
one already serving `secr3t.dopesoft.io` (SSH) and `vnc.dopesoft.io` (VNC).
We just add a third ingress rule for `coder.dopesoft.io`. One tunnel,
one daemon, no duplication.

## Prereqs

- macOS 14+, on a desk Mac (or Mac mini) plugged in 24/7.
- Existing cloudflared system daemon already up — `launchctl list | grep cloudflared`
  should show `com.cloudflare.cloudflared`.
- Logged in to your `claude` CLI with a **Max** plan: `claude /login` interactive.
  Verify: `claude /status` shows `oauth subscription`.
- Homebrew + Node 20+.

---

## 0. Install the user-scope agents

```sh
bash docs/claude-code/install.sh
```

Idempotent. It does:

1. Verifies `cloudflared`, `mcp-proxy`, `claude` are on `PATH` (installs `mcp-proxy` if missing).
2. Generates a 32-byte bearer token in `~/.config/infinity/coder.env` (reused on re-run).
3. Removes any legacy `dev.dopesoft.cloudflared` user-LaunchAgent (avoids clashing with the system daemon).
4. Installs three LaunchAgents to `~/Library/LaunchAgents/`:
   - `dev.dopesoft.claude-mcp` — log namespace placeholder
   - `dev.dopesoft.mcp-proxy` — runs `mcp-proxy --sse-port 8765 -- claude mcp serve --dangerously-skip-permissions`
   - `dev.dopesoft.caffeinate` — `caffeinate -dimsu`
5. Bootstraps them with `launchctl`.

Verify:

```sh
launchctl list | grep dopesoft
# expect: claude-mcp, caffeinate, mcp-proxy
```

## 1. Add `coder.dopesoft.io` ingress to the existing tunnel

The repo already wrote the new ingress into `~/.cloudflared/config.yml`. The
system daemon reads `/etc/cloudflared/config.yml` instead, so promote it:

```sh
sudo cp ~/.cloudflared/config.yml /etc/cloudflared/config.yml
sudo launchctl kickstart -k system/com.cloudflare.cloudflared
```

After kickstart you should see four routes registered (in cloudflared logs at
`/Library/Logs/com.cloudflare.cloudflared.err.log`):

- `coder.dopesoft.io → http://127.0.0.1:8765`
- `secr3t.dopesoft.io → ssh://localhost:22`
- `vnc.dopesoft.io → http://localhost:6901`
- `http_status:404` (catch-all)

## 2. Route DNS

```sh
cloudflared tunnel route dns 8e5bd68f-e416-42c8-a0f2-0a8fce21d976 coder.dopesoft.io
```

This adds a CNAME `coder` → `<tunnel-uuid>.cfargotunnel.com` in the Cloudflare
DNS table for `dopesoft.io`. Check it appeared next to `secr3t` / `vnc` /
`agents` in the dashboard.

## 3. Cloudflare Access policy (Zero Trust)

In the **Zero Trust dashboard** at <https://one.dash.cloudflare.com/> (not the
regular DNS dashboard) → **Access** → **Applications** → **Add an application**:

- **Type**: Self-hosted
- **Application name**: `Infinity Coder`
- **Application domain**: subdomain `coder`, domain `dopesoft.io`
- **Identity providers**: Google Workspace (or whatever you use)
- **Policy 1 — Boss email allow**:
  - Action: `Allow`
  - Include: `Emails == kai@dopesoft.io`
- **Policy 2 — Railway core service-token**:
  - First, create the token: **Access** → **Service Auth** → **Service Tokens**
    → **Create Service Token**. Name it `infinity-railway-core`. Cloudflare
    shows the `Client ID` and `Client Secret` exactly once — copy both now.
  - Back on the application's Policies tab, add a second policy:
    - Action: `Service Auth`
    - Include: `Service Token == infinity-railway-core`

Two policies because: humans browsing get IDP auth; Railway core sends the
Service Token headers (`CF-Access-Client-Id` + `CF-Access-Client-Secret`).
Both routes hit the same backend. **Do not** add a Service Auth policy to
`secr3t` or `vnc` — those are already covered by their own existing policies.

## 4. Set Railway env vars

```sh
railway variables --service core \
  --set CLAUDE_CODE_TUNNEL_URL=https://coder.dopesoft.io/sse \
  --set CF_ACCESS_CLIENT_ID="<paste client id>.access" \
  --set CF_ACCESS_CLIENT_SECRET="<paste client secret>"
```

(Cloudflare Service Token client IDs come with a `.access` suffix — keep it.)

Then in `core/config/mcp.yaml` flip `claude_code.enabled: true` and redeploy
core. On boot you should see:

```
mcp: claude_code connected (7 tools)
```

## 5. Verify end-to-end

From your laptop (away from the home Mac):

```sh
curl -N \
  -H "CF-Access-Client-Id: <client id>.access" \
  -H "CF-Access-Client-Secret: <client secret>" \
  https://coder.dopesoft.io/sse | head -3
# Expect: text/event-stream chatter from mcp-proxy.
```

Then in Studio:

> "list the files in ~/Dev/infinity"

The agent calls `claude_code__bash` (in the default block list → queues a Trust
contract). Approve in the Trust tab. The call fires, output streams back.

For genuinely read-only ops (`grep`, `ls`, `glob`) the gate auto-allows.

## 6. Trust gating policy

Configured via env on **core**:

| Var | Default | Meaning |
|---|---|---|
| `INFINITY_CLAUDE_CODE_BLOCK` | `bash,write,edit` | tool suffixes that always queue |
| `INFINITY_CLAUDE_CODE_AUTOAPPROVE` | _empty_ | tool suffixes that always allow even if blocked |

To go YOLO while iterating (don't ship):

```sh
railway variables --service core \
  --set INFINITY_CLAUDE_CODE_AUTOAPPROVE=bash,write,edit,read,ls,grep,glob
```

## 7. Permission prompts

In `claude mcp serve` mode, Claude Code does **not** prompt for permission —
the parent MCP client (Infinity) is the authority. The CLI just exposes its
tools and lets the client decide. That's why no `--dangerously-skip-permissions`
flag is needed (and it isn't accepted on this subcommand).

Infinity's `ClaudeCodeGate` (`core/internal/proactive/gate.go`) is the
real gate: anything in `INFINITY_CLAUDE_CODE_BLOCK` (default `bash,write,edit`)
gets queued in `mem_trust_contracts` for the boss to approve in Studio.

## 8. Failure modes & recovery

| Symptom | Cause | Fix |
|---|---|---|
| `mcp: claude_code failed: dial: timeout` | Mac asleep / Wi-Fi hiccup | `caffeinate` daemon should keep it awake. cloudflared auto-reconnects. |
| `mcp: claude_code failed: 401` | Service Token mismatch | Rotate it: Zero Trust → Access → Service Auth → Service Tokens → Refresh. Paste the new client-id + secret into Railway core env. |
| `claude /status` shows expired | OAuth lapsed (rare) | `claude /login` once interactively on the Mac. |
| `Tool exited with status 1` | Real Claude Code failure | Check `~/Library/Logs/infinity-coder/mcp-proxy.log` and `mcp-proxy.err`. |
| Mac reboot, agents don't start | LaunchAgents need user login. | System Settings → Users & Groups → enable Auto-login. |
| `coder.dopesoft.io` 404s | Ingress not picked up | `sudo launchctl kickstart -k system/com.cloudflare.cloudflared` and re-check `/etc/cloudflared/config.yml`. |

## 9. Working directory

`mcp-proxy` starts `claude mcp serve` with cwd = `$HOME/Dev`. The agent can
`cd` into any project under there per call. To swap dirs without restarting,
use `claude_code__bash` with `cd /elsewhere && ...`.

## Files in this directory

- `install.sh` — idempotent setup (excludes cloudflared — system daemon owns it)
- `launchd/dev.dopesoft.claude-mcp.plist`
- `launchd/dev.dopesoft.mcp-proxy.plist`
- `launchd/dev.dopesoft.caffeinate.plist`

Read each plist before loading. They reference `__HOME__` which `install.sh`
substitutes with `$HOME`.
