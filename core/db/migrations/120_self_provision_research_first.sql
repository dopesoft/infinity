-- 120_self_provision_research_first.sql
--
-- The cloud-workspace skill (070) taught Jarvis HOW to run an install
-- (`pip install`, `npm i -g`, `apt-get`, `curl … | sh`) but never told him to
-- find the RIGHT method first. On 2026-06-03, asked to install the Higgsfield
-- CLI, he guessed `pip install higgsfield || npm install -g higgsfield` — both
-- 404. higgsfield ships as a Homebrew-tap / GoReleaser RELEASE BINARY, on
-- neither registry. He never thought to search the web first. The boss's words:
-- "he didnt even think let me search the web, ohh its the cli.. that was a
-- silly miss."
--
-- Fix (Rule #1b): the missing piece is JUDGMENT — research before acting — so it
-- goes in the skill, not in Go. The install/PATH plumbing already lives in
-- core/internal/extensions/cli.go ($HOME/.local/bin on PATH via the persistent
-- env.sh). We surgically insert a "research the method first" subsection ahead of
-- the existing "Make it PERSIST" section of the active cloud-workspace version.
--
-- Idempotent: the NOT LIKE guard makes a re-run a no-op.

BEGIN;

UPDATE mem_skill_versions
SET skill_md = replace(
  skill_md,
  $anchor$### Make it PERSIST (this is the important part)$anchor$,
  $new$### Research the method BEFORE you install (never guess)

When you're asked to install a tool you don't already know how to install, your
FIRST move is to look up the official method — `websearch`, then `http_fetch` the
project's install page or GitHub README. Outbound fetch is unrestricted, so there
is no excuse to guess. NEVER blind-fire `pip install <x> || npm install -g <x>`
hoping one sticks — that exact guess is how the Higgsfield install failed twice:
it ships as a Homebrew-tap / GoReleaser **release binary**, not a pip or npm
package. Go and Rust CLIs are usually the same — a GitHub release tarball or a
`curl … | sh` installer, not a language package.

You do NOT need Homebrew on this Linux box. If a tool says "install via Homebrew",
open its tap formula — it almost always just downloads a prebuilt release binary
you can fetch directly. Pull the `linux_amd64` asset straight into
`$HOME/.local/bin` (already on PATH via the persistent env), e.g.
`curl -fsSL <release-url>.tar.gz | tar xz -C "$HOME/.local/bin" <binary>`, then
prove it with `command -v <binary>` and `<binary> version`.

For anything you'll reuse, register it with `extension_register kind=cli`: it pins
the install + credentials to the volume and runs the device-login flow for you —
hand the boss the auth URL it returns and it picks up automatically once he's in.

### Make it PERSIST (this is the important part)$new$
)
WHERE skill_name = 'cloud-workspace'
  AND version = 'v1.0-5-24-2026'
  AND skill_md NOT LIKE '%Research the method BEFORE you install%';

COMMIT;
