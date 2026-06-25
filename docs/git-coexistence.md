# Sharing `main` with the autonomous bot (no more "divergent branches")

Infinity's self-improve bot commits **and deploys to `main` unattended** — that's
the product, not a bug, and it stays that way. The cost of that autonomy is that
you and the bot both write the same branch, so a naive `git push` can hit
`divergent branches / Need to specify how to reconcile`. This is how the two
sides coexist without ever clipping the bot.

## What the bot does (already enforced in code)

Every autonomous push now goes through `safePush` (in `tools/mcp-bridge/exec.go`,
`docker/workspace/main.go`) and the sentry's `gitRevertToGood`
(`docker/sentry/main.go`):

- **fetch → rebase → push**, never a blind push, **never `--force`**.
- On a rebase conflict it **`git rebase --abort`** (leaves the tree clean) and
  fails loud — it never leaves your checked-out clone mid-rebase.
- It **never rebases over uncommitted changes** — with a dirty tree it skips the
  rebase and lets a non-fast-forward push fail loud instead of touching your work.
- The sentry watchdog reverts **only the bot's own `[self-improve]` commits**, and
  a deterministic veto blocks any revert/commit that would delete or gut the
  error-visibility machinery (files carrying `honesty-machinery: do-not-revert`).

So the bot will rebase onto your work rather than collide with it. That alone
resolves the divergence from the bot's side.

## What you do (one-time, per machine)

`pull.rebase=true` is set on whatever clone ran the fix, but git config is
per-machine — set it everywhere you work, and add a one-shot `sync`:

```sh
# Always rebase local commits onto upstream instead of creating a merge bubble.
git config --global pull.rebase true

# `git sync` = fetch + rebase your work on top + push, in one step.
git config --global alias.sync '!git pull --rebase && git push'
```

Then your normal flow is just:

```sh
git add -A && git commit -m "…"
git sync
```

If `git sync` ever stops on a conflict, resolve the files, `git add` them, then
`git rebase --continue` (or `git rebase --abort` to back out) and `git push`.

## Why not branch-protect the bot out of `main`?

Because requiring a human PR approval on the bot's commits would kill the
unattended commit-and-deploy autonomy that is the entire point of the
self-improve loop. Coexistence is achieved by the bot **rebasing**, not by
gating it. See `CLAUDE.md` ("self-healing & error visibility") and the memory
`project_sentry_blind_revert` for the full rationale.
