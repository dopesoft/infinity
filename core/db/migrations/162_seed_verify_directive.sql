-- Steal C, Lever 3: seed the adversarial-verify directive as DATA (Rule #1b).
-- The agent loop fires ONE bounded "red-team your own answer" pass on high/xhigh
-- effort turns; the MECHANIC (when to fire, the cap, no stacking with self-heal)
-- lives in Go (core/internal/agent/effort_pass.go), but the JUDGMENT prose lives
-- here in infinity_meta so it is versioned, visible, and improvable by
-- Voyager/GEPA — never frozen in a Go const. serve.go reads this key at boot via
-- settings.Store.Get and calls loop.SetVerifyDirective. ON CONFLICT DO NOTHING so
-- a boss edit (or a later GEPA-promoted revision) is never clobbered by re-running
-- the migrator.
INSERT INTO infinity_meta (key, value, updated_at)
VALUES (
  'verify_directive',
  'Before you finish: red-team your own answer above. Find the single strongest flaw, missing edge case, wrong assumption, or unverified claim in it. If it holds up, confirm it in one line and stop. If it does not, correct it now — do not restate the whole answer, just fix what is wrong. Be terse and honest; this is a self-check, not a do-over.',
  now()
)
ON CONFLICT (key) DO NOTHING;
