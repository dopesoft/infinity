-- 079_inbox_triage_cron_and_pointer.sql
--
-- Clean the inbox-triage skill down to ONE canonical version under the boss's
-- date-stamped naming convention (vX.Y-M-D-YYYY, e.g. v1.0-5-25-2026), and make
-- the 6h cron actually execute the recipe instead of wandering or aborting.
-- Pairs with Go fixes in skills/runner.go (imperative FormatLLMPrompt framing),
-- soul.md (principle 8: a recipe is instructions to execute, not a result), and
-- skills/store.go (ListVersions now hides archived_at rows so this cleanup is
-- visible in Studio's version history).
--
-- WHY this matters: a GEPA candidate `v1.0-5-25-2026` was pinned active but its
-- skill_md was missing the YAML frontmatter that the canonical 1.2.0 body
-- carries — including the "Never resolves a follow-up itself - that is the
-- boss's call" rule (his hard line from the 2026-05-24 auto-dismiss incident),
-- the `surfaced` output, and extra triggers. The prose bodies are IDENTICAL;
-- 1.2.0 = the approved body + that frontmatter. So we keep the boss's chosen
-- name and FOLD the complete body into it, rather than flipping to the
-- off-convention semver name.
--
-- Five steps, all reversible:
--   1. Cron prompt rewrite — kill the claude_code/bash/filesystem detour
--      (live runs opened with claude_code__Bash before reaching skills_invoke,
--      burning ~14s and wrongly routing a Gmail task at the Mac bridge).
--   2. Fold the canonical 1.2.0 body+frontmatter into v1.0-5-25-2026 (rewriting
--      the frontmatter version string to match), so the kept version carries
--      the never-resolve rule and full metadata.
--   3. Update the mem_skills.description to the complete canonical one.
--   4. Keep v1.0-5-25-2026 active AND pin it, so GEPA/auto-promotion can't
--      silently re-introduce version drift (the downstream risk the boss is
--      strict about).
--   5. Archive the off-convention duplicate versions (1.0.0, 1.1.0, 1.2.0,
--      v1.0-5-19-2026) via archived_at, and reject the stray classification-only
--      fix proposal the aborted run filed. Soft retire, not delete.

BEGIN;

-- 1. Cron prompt: pin skills_invoke first, forbid code/shell/fs detours.
UPDATE mem_crons
SET target = 'Triage the boss''s Gmail inbox now. Your FIRST action must be skills_invoke with name="inbox-triage" — then EXECUTE every step it returns this turn using the Composio Gmail tools (composio__GMAIL_*) and the Trust/surface tools. The returned recipe is your instruction set, not a result; do not stop after reading it. This is a Gmail + dashboard task only: do NOT use claude_code, bash, the filesystem, or git — there is no code to run or files to read here. Pick the active connected_account from the <connected_accounts> overlay whose identity matches kai@dopesoft.io; if no usable Gmail account exists, exit cleanly and surface a finding instead of erroring. Cap max_messages at 20. Surface each message awaiting the boss''s reply to the Follow-ups dashboard, queue any drafts under one fresh batch_id, then report the {classified, drafted, surfaced, batch_id, skipped} summary. Never send mail directly — drafts only. Never dismiss or resolve a follow-up — that is the boss''s call.'
WHERE name = 'inbox-triage';

-- 2. Fold the canonical 1.2.0 body+frontmatter into the boss's named version,
--    rewriting the frontmatter version string so it self-describes correctly.
UPDATE mem_skill_versions tgt
SET skill_md = replace(src.skill_md, 'version: "1.2.0"', 'version: "v1.0-5-25-2026"'),
    promoted_at = NOW()
FROM (SELECT skill_md FROM mem_skill_versions
      WHERE skill_name = 'inbox-triage' AND version = '1.2.0') src
WHERE tgt.skill_name = 'inbox-triage' AND tgt.version = 'v1.0-5-25-2026';

-- 3. Canonical description (carries the never-resolve intent in skills_list).
UPDATE mem_skills
SET description = 'The single canonical Gmail triage recipe. Classify recent mail, SURFACE the messages awaiting the boss''s reply to the Follow-ups dashboard (deduped, importance-ranked, full body captured durably), AND draft replies queued under one Trust batch. Multi-account aware. Never resolves a follow-up itself - that is the boss''s call.'
WHERE name = 'inbox-triage';

-- 4. Keep the boss's named version active AND pin it against future drift.
INSERT INTO mem_skill_active (skill_name, active_version, pinned)
VALUES ('inbox-triage', 'v1.0-5-25-2026', TRUE)
ON CONFLICT (skill_name) DO UPDATE
  SET active_version = 'v1.0-5-25-2026',
      pinned         = TRUE,
      updated_at     = NOW();

-- 5a. Archive the off-convention duplicate versions (soft retire).
UPDATE mem_skill_versions
SET archived_at = NOW()
WHERE skill_name = 'inbox-triage'
  AND version IN ('1.0.0', '1.1.0', '1.2.0', 'v1.0-5-19-2026')
  AND archived_at IS NULL;

-- 5b. Reject the obsolete classification-only fix proposal.
UPDATE mem_skill_proposals
SET status = 'rejected'
WHERE id = '2fdfe7e6-a838-4293-b0c0-ee84bbc9fa14'
  AND status IN ('candidate', 'pending', 'draft');

COMMIT;
