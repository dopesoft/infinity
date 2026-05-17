-- 009_canvas_projects.sql - Sessions become first-class projects.
--
-- Adds five columns to mem_sessions:
--   name             - short LLM-generated label ("Building chat app with vite").
--   project_path     - absolute path on the Mac where the app lives.
--   project_template - slug of the scaffold skill that created it (nextjs, vite,
--                      static, ios-swift, capacitor) or NULL for non-coding chats.
--   dev_port         - last-known dev server port for the supervisor to bind to.
--   last_run_at      - most recent time the supervisor reported the project running.
--
-- Empty strings / NULLs are valid - the session may exist long before any
-- scaffold call attaches a project. The Studio Canvas tab branches on
-- project_path: present → file tree + preview, absent → "no app yet" state.

BEGIN;

ALTER TABLE mem_sessions
    ADD COLUMN IF NOT EXISTS name             TEXT,
    ADD COLUMN IF NOT EXISTS project_path     TEXT,
    ADD COLUMN IF NOT EXISTS project_template TEXT,
    ADD COLUMN IF NOT EXISTS dev_port         INT,
    ADD COLUMN IF NOT EXISTS last_run_at      TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS mem_sessions_name_idx
    ON mem_sessions(name) WHERE name IS NOT NULL;
CREATE INDEX IF NOT EXISTS mem_sessions_project_path_idx
    ON mem_sessions(project_path) WHERE project_path IS NOT NULL;
CREATE INDEX IF NOT EXISTS mem_sessions_last_run_idx
    ON mem_sessions(last_run_at DESC NULLS LAST);

INSERT INTO infinity_meta (key, value)
VALUES ('canvas_projects_initialized_at', NOW()::TEXT)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

COMMIT;
