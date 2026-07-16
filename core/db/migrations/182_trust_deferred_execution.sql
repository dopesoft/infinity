-- 182_trust_deferred_execution.sql - record whether an approved contract
-- actually RAN, and what happened when it did.
--
-- Why this exists. Until now "the boss approved it" and "the action
-- happened" were the same event, because the only path from approval to
-- execution ran inside the gate's own wait loop: the gate queues a
-- contract, blocks in WaitForDecision for its WaitTimeout (15 min in
-- GitHubGate), and if the approval lands inside that window it calls
-- ConsumeApprovedForTool and the tool runs in-session.
--
-- Approve LATER and the two events come apart. The gate has already timed
-- out, the session is gone, and HasRecentApprovalForTool /
-- ConsumeApprovedForTool are scoped to (action_spec->>'tool',
-- action_spec->>'session_id') - so no future session ever matches the dead
-- session id. The row sits at status='approved' forever and the action the
-- boss said yes to silently never happens. TrustExecutor
-- (core/internal/proactive/trust_executor.go) closes that gap by claiming
-- those orphans and running them out-of-band.
--
-- Once execution is decoupled from approval, status alone can no longer
-- answer "did this actually run?" - 'consumed' is written by BOTH the
-- in-session gate path and the deferred executor, and a tool that was
-- claimed but then failed (tool no longer registered, API 500) would look
-- identical to one that succeeded. That is exactly the false-green the
-- honesty guards exist to prevent, so the outcome gets its own columns
-- rather than being inferred.
--
-- Generic on purpose: every gate (github, composio, claude_code, bridge,
-- browser, phone, ward) shares mem_trust_contracts, so every gate inherits
-- deferred execution and this ledger. No per-tool column, no per-source
-- branch.

BEGIN;

ALTER TABLE mem_trust_contracts
  -- NULL = never executed out-of-band. Set only by TrustExecutor, so a row
  -- with status='consumed' AND executed_at IS NULL is one the in-session
  -- gate ran itself. Distinguishing the two is what makes the audit trail
  -- honest about which path fired.
  ADD COLUMN IF NOT EXISTS executed_at TIMESTAMPTZ,
  -- Empty = the deferred run succeeded. Non-empty = it was claimed and
  -- FAILED, and the text is why. A failure must be visibly a failure; the
  -- boss approved something that then didn't happen, and that is worth
  -- surfacing rather than burying.
  ADD COLUMN IF NOT EXISTS execution_error TEXT NOT NULL DEFAULT '';

-- The executor's claim query filters on (status, created_at) and is polled
-- on an interval, so keep it off a seq scan as the contract table grows.
-- Partial: only 'approved' rows are ever candidates.
CREATE INDEX IF NOT EXISTS idx_trust_approved_pending_exec
    ON mem_trust_contracts(created_at)
    WHERE status = 'approved';

COMMIT;
