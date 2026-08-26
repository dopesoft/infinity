-- 190_attachments.sql - files the boss hands Jarvis in chat.
--
-- Before this, a chat attachment was metadata only: Studio sent
-- {name, mime_type, size_bytes} and Core stuffed a one-line "Attached files:"
-- note into the prompt. Jarvis was TOLD a file existed and never received it.
--
-- mem_attachments is the durable home for the bytes. Postgres holds the
-- source of truth so a turn can always build native multimodal content
-- (image / PDF blocks) for the brain, independent of whether the cloud
-- workspace is reachable. The workspace gets a best-effort mirror
-- (workspace_path) so Jarvis can bash on the file (pdftotext, OCR, python).
--
-- kind='page' rows are rasterized pages of a parent PDF (scanned documents
-- with no text layer) so a text-only brain still SEES the document.
--
-- Every upload is also indexed as a mem_artifacts row (storage_kind =
-- 'postgres', storage_path = 'attachment:<id>') so artifact_get / artifact_list
-- are the canonical way for the agent to find it again in a later turn.

CREATE TABLE IF NOT EXISTS mem_attachments (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id     TEXT NOT NULL,
    kind           TEXT NOT NULL DEFAULT 'upload' CHECK (kind IN ('upload', 'page')),
    parent_id      UUID REFERENCES mem_attachments(id) ON DELETE CASCADE,
    page_no        INT,
    name           TEXT NOT NULL,
    mime           TEXT NOT NULL DEFAULT 'application/octet-stream',
    size_bytes     BIGINT NOT NULL,
    sha256         TEXT NOT NULL,
    bytes          BYTEA NOT NULL,
    -- Derived text (pdftotext / office → pdf → text / utf-8 passthrough).
    text_extract   TEXT,
    extract_status TEXT NOT NULL DEFAULT 'pending'
                   CHECK (extract_status IN ('pending', 'ok', 'empty', 'failed', 'skipped')),
    extract_error  TEXT,
    page_count     INT,
    -- Best-effort mirror on the cloud workspace volume (/workspace/uploads/…).
    workspace_path TEXT,
    artifact_id    UUID REFERENCES mem_artifacts(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_attachments_session
    ON mem_attachments(session_id, created_at DESC) WHERE kind = 'upload';
CREATE INDEX IF NOT EXISTS idx_mem_attachments_parent
    ON mem_attachments(parent_id, page_no) WHERE parent_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_mem_attachments_sha
    ON mem_attachments(sha256);
