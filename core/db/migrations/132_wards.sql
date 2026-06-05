-- 132_wards.sql — Wards: structural privacy zones on file-touching tools.
--
-- memory.StripSecrets redacts secrets at the CAPTURE boundary (what we store).
-- It does nothing about what the agent may READ in the first place. A Ward is a
-- declared path pattern the agent must not freely read: 'private' → the read is
-- denied outright; 'sensitive' → it routes through the Trust queue for the boss
-- to approve. Enforced by proactive.WardGate in the gate chain (Go, not prose),
-- on claude_code__read/edit/write, filesystem__read_*, and any bash command that
-- names a warded path.
--
-- The credential/key/.env defaults ship seeded so they are live on first boot —
-- the boss never has to think to protect them.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_wards (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    -- a glob/substring pattern matched against the path the tool wants to touch.
    glob       TEXT NOT NULL,
    -- private  → deny the read
    -- sensitive → queue a Trust contract (boss approves per the window)
    level      TEXT NOT NULL DEFAULT 'private'
               CHECK (level IN ('private','sensitive')),
    note       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (glob)
);

-- Seed the obvious credentials/keys/.env so they're protected from boot.
INSERT INTO mem_wards (glob, level, note) VALUES
    ('.credentials.json', 'private', 'Claude Code OAuth credentials — never read'),
    ('*/.env',            'private', 'Environment files (secrets)'),
    ('.env',              'private', 'Environment files (secrets)'),
    ('*.pem',             'private', 'Private keys / certs'),
    ('*.key',             'private', 'Private keys'),
    ('id_rsa',            'private', 'SSH private key'),
    ('id_ed25519',        'private', 'SSH private key')
ON CONFLICT (glob) DO NOTHING;

-- Realtime: the Settings → Privacy ward list live-updates.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime') THEN
        IF NOT EXISTS (
            SELECT 1 FROM pg_publication_tables
             WHERE pubname = 'supabase_realtime'
               AND schemaname = 'public'
               AND tablename = 'mem_wards'
        ) THEN
            ALTER PUBLICATION supabase_realtime ADD TABLE public.mem_wards;
        END IF;
    END IF;
END $$;

COMMIT;
