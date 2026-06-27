-- 163_fix_make_document_duplicate.sql
--
-- Fix the duplicate document artifact. `document_create` already records a
-- mem_artifacts row deterministically (server.recordDocumentArtifact — with a
-- thumbnail + preview metadata, virtual_path /artifacts/<file>). But the
-- make-document skill ALSO told the agent to call `artifact_save` (§5), which
-- inserted a SECOND row (model-chosen path, friendly name, NO preview metadata)
-- — so every doc showed up twice in the Media gallery, the second one an
-- icon-only, download-only entry. Confirmed on prod: the DFW deck had two rows
-- at the same minute, /artifacts/…(+thumb) and /documents/…(no thumb).
--
-- Rule #1b: recording the artifact is a CODE mechanic (recordDocumentArtifact);
-- the skill must NOT also do it in prose. This bumps make-document to v1.3,
-- removing the artifact_save instruction (§5) and the `artifact_id` output that
-- nudged the model toward calling it.
--
-- Safe + verifiable: derives the new body from the live v1.2 row (161) via
-- replace(); only repoints mem_skill_active when v1.3 exists, so it's a clean
-- no-op if 161 hasn't been applied.

BEGIN;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
SELECT
  'make-document',
  'v1.3-6-26-2026',
  replace(
    replace(
      replace(
        skill_md,
        'version: "v1.2-6-25-2026"',
        'version: "v1.3-6-26-2026"'
      ),
      $old_out$  - name: filename
    type: string
  - name: artifact_id
    type: string$old_out$,
      $new_out$  - name: filename
    type: string$new_out$
    ),
    $old5$## 5. Index it so it surfaces

After it's created, call `artifact_save` with `kind: "document"`, the
returned `path` as `storage_path`, `storage_kind: "workspace"`, a friendly
`name`, and a `virtual_path` (e.g. `decks/agent-factory.pptx`). This is
what makes it show up in the boss's artifacts and open in Studio.$old5$,
    $new5$## 5. It's already saved — do NOT call artifact_save

`document_create` automatically indexes the document in the boss's
artifacts/Media gallery (with a thumbnail and inline preview). Do NOT also
call `artifact_save` for the document — that creates a duplicate entry (a
second tile with no preview that can only download). There is nothing to do in
this step.$new5$
  ),
  implementation,
  confidence,
  'manual'
FROM mem_skill_versions
WHERE skill_name = 'make-document' AND version = 'v1.2-6-25-2026'
ON CONFLICT (skill_name, version) DO NOTHING;

UPDATE mem_skill_active
   SET active_version = 'v1.3-6-26-2026'
 WHERE skill_name = 'make-document'
   AND EXISTS (
     SELECT 1 FROM mem_skill_versions
      WHERE skill_name = 'make-document' AND version = 'v1.3-6-26-2026'
   );

COMMIT;
