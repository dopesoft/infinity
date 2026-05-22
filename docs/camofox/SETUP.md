# Camoufox anti-detect browser — setup

Jarvis's `browser_*` tools (open / navigate / observe / act / extract / close +
live Preview screencast) sit behind a swappable **Backend** interface
([`core/internal/browser/backend.go`](../../core/internal/browser/backend.go)).
This wires the **Camoufox** anti-detect engine ([redf0x1/camofox-browser](https://github.com/redf0x1/camofox-browser))
as that backend on **both bridges** — the home Mac (residential IP) and the
Cloud (Railway) — so Jarvis gets through Cloudflare / DataDome / bot-walls that
block the plain chromedp `browser` sidecar.

**Why both, and why Mac-first.** Camoufox spoofs the *fingerprint* at the C++
engine level, but not the *IP*. WAFs also score IP reputation, so a datacenter
IP undercuts the spoofing. The home Mac runs on a **residential IP** — that's
where anti-detect actually lands — so Core prefers it whenever it's reachable
([`routing.go`](../../core/internal/browser/routing.go)) and falls back to Cloud
when the Mac is offline.

```
browser_* tools ──▶ browser.Client (Backend)
                        │   RoutingBackend: Mac-first, Cloud fallback
                        ├── Mac:   camofox @ camo.dopesoft.io  (Cloudflare Access, residential IP)  ◀ preferred
                        └── Cloud: camofox @ camofox.railway.internal:9377  (private net, bearer)
```

Nothing in Go is per-vendor: the adapter ([`camofox.go`](../../core/internal/browser/camofox.go))
maps our verbs onto Camoufox's REST contract, and the *how-to-browse* cognition
lives in the seeded `web-browsing` skill (migration
[`065`](../../core/db/migrations/065_seed_browser_skill_stealth.sql)), not here.

---

## 1. Cloud instance (Railway)

`docker/camofox/Dockerfile` is a thin pin over the upstream GHCR image, already
registered as the `camofox` service in [`railway.toml`](../../railway.toml).

1. Create the Railway service `camofox` with **root directory `docker/camofox`**
   (the dashboard auto-detects the Dockerfile).
2. Set env on the **camofox** service:
   ```
   CAMOFOX_API_KEY=<generate a strong secret>
   ```
   `CAMOFOX_HOST=0.0.0.0` and `CAMOFOX_PORT=9377` are baked into the Dockerfile.
   Optional residential exit (recommended — see note above):
   ```
   PROXY_HOST=... PROXY_PORT=... PROXY_USERNAME=... PROXY_PASSWORD=...
   ```
3. Set env on the **core** service:
   ```
   CAMOFOX_API_KEY=<same secret as the camofox service>
   # CAMOFOX_URL auto-defaults to http://camofox.railway.internal:9377 on Railway
   # when CAMOFOX_API_KEY is set — set it explicitly only to override.
   CAMOFOX_USER_ID=jarvis     # optional; one stable canonical fingerprint owner
   ```
4. Enable **App Sleeping** on `camofox` so it scales to zero when idle. The
   first browse after sleep wakes it (~10–20s, Firefox cold start) and Core's
   HTTP client retries.

That alone gives Jarvis the anti-detect browser. The Mac instance below is the
stronger path and is what makes "bypass the block" reliably work.

---

## 2. Mac instance (residential IP — the strong path)

Run the **same pinned image** on the home Mac and expose it through the
**existing Cloudflare Tunnel + Access** you already use for the `claude_code`
bridge (so it reuses the same `CF_ACCESS_*` service token). Docker keeps the
Mac on the exact same version as Cloud — one version knob.

### a. Run the container (launchd)

Install the plist:
```bash
cp docs/camofox/launchd/dev.dopesoft.camofox.plist ~/Library/LaunchAgents/
# put your secret where the plist reads it (matches the Cloud CAMOFOX_API_KEY):
launchctl setenv CAMOFOX_API_KEY '<same secret as Cloud>'
launchctl load -w ~/Library/LaunchAgents/dev.dopesoft.camofox.plist
```
It runs `ghcr.io/redf0x1/camofox-browser:2.4.3` bound to `127.0.0.1:9377` with a
persistent profile volume (`~/.camofox`) so logins/cookies survive restarts.
Verify: `curl -s localhost:9377/health` → `{"ok":true,...}`.

### b. Tunnel route + Access

Add a hostname to your existing `cloudflared` config (same tunnel as
`coder.dopesoft.io`):
```yaml
ingress:
  - hostname: camo.dopesoft.io
    service: http://localhost:9377
  # ... existing claude_code route ...
  - service: http_status:404
```
`cloudflared service restart`, add the `camo.dopesoft.io` DNS route, then in
Cloudflare Access create/extend an application covering `camo.dopesoft.io` that
accepts the **same service token** as the claude_code bridge.

### c. Point Core at it

On the **core** service:
```
CAMOFOX_URL_MAC=https://camo.dopesoft.io
# CF_ACCESS_CLIENT_ID / CF_ACCESS_CLIENT_SECRET — already set for claude_code, reused as-is
CAMOFOX_API_KEY=<same secret>     # the bearer rides under the CF Access headers
```

With both `CAMOFOX_URL` (Cloud) and `CAMOFOX_URL_MAC` set, Core builds the
`RoutingBackend`: Mac-first, Cloud fallback. Boot log: `browser: camoufox routed
(mac=… + cloud=…, mac-first)`.

---

## 3. Apply the skill migration

```bash
cd core && railway run --service core -- go run ./cmd/infinity migrate
```
Confirm `apply 065_seed_browser_skill_stealth.sql` in the output. This activates
`web-browsing` **v1.1** (anti-detect awareness + the escalation recipe + the
domain-playbook self-heal). Idempotent — safe to re-run.

---

## 4. Updating when redf0x1 ships a new version

This is deliberately a one-line change:

1. Bump `ARG CAMOFOX_VERSION=` in [`docker/camofox/Dockerfile`](../../docker/camofox/Dockerfile)
   to the new upstream tag.
2. Redeploy the Cloud `camofox` service; on the Mac, edit the image tag in the
   plist and `launchctl kickstart -k` it (or `docker pull` the new tag).

**Our Go adapter does not change.** It targets Camoufox's stable REST contract
(`/tabs`, `/navigate`, `/snapshot`, `/act`, `/tabs/:id/evaluate`,
`/tabs/:id/screenshot`, `/health`), not their internals — and upstream ships
backward-compatible aliases for any moved endpoint. If a release *does* change a
wire shape, the only file that needs touching is
[`camofox.go`](../../core/internal/browser/camofox.go); everything else (tools,
routing, registry, Studio Preview) is insulated by the `Backend` interface.
Watch their [CHANGELOG / RELEASE_NOTES](https://github.com/redf0x1/camofox-browser/blob/main/CHANGELOG.md)
on a bump.

---

## Rollback

Unset `CAMOFOX_URL`, `CAMOFOX_URL_MAC`, and `CAMOFOX_API_KEY` on the core
service. Core falls straight back to the chromedp `browser` sidecar (or to no
browser if that's also unset). No code change, no redeploy of core needed beyond
the env change.
