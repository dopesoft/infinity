-- 187_backfill_call_times.sql - give the calls already in the log their times.
--
-- The call viewer now shows when a call rang, when it hung up, and how long it
-- ran, read from structured metadata (started_at / ended_at / duration_ms) that
-- phone/monitor.go stamps on every new call from here on.
--
-- The 12 calls already logged have metadata = '{}'. Their duration was only ever
-- recorded as PROSE: the "4m36s · " prefix Go glues onto the subtitle for display
-- (`fmt.Sprintf("%s · %s", dur.Round(time.Second), ...)`). So recovering it means
-- a regex over a display string, which is the whole argument for not storing data
-- that way. Doing it once here so his existing log isn't blank.
--
-- ended_at = created_at is sound, not a guess: deliverOutcome upserts this row
-- the instant the call ends, so the row's birth IS the hang-up.
--
-- Idempotent: re-running recomputes the same values from the same subtitle.

BEGIN;

UPDATE mem_surface_items si
   SET metadata = COALESCE(si.metadata, '{}'::jsonb) || jsonb_build_object(
         'started_at',  to_char((si.created_at - d.dur) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
         'ended_at',    to_char(si.created_at         AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
         'duration_ms', (EXTRACT(EPOCH FROM d.dur) * 1000)::bigint
       ),
       -- NOT updated_at: the card renders relTime(updatedAt ?? createdAt), so
       -- touching it here would relabel every call with the age of this
       -- migration. That is exactly the bug 185 shipped and 186 had to undo.
       updated_at = si.updated_at
  FROM (
        SELECT id,
               -- "4m36s" / "2m1s" / "51s" / "1h2m3s" -> a real interval.
               -- Go's duration format has no spaces, so expanding each unit
               -- letter into its word gives Postgres something it can cast:
               -- "4m36s" -> "4 minutes 36 seconds".
               --
               -- ORDER IS LOAD-BEARING: s, then m, then h. Each replacement
               -- inserts a word that later passes would re-scan, so every unit
               -- letter must be spent before a word containing it appears.
               -- Doing h/m/s (the obvious order) turns "4m36s" into
               -- "4 minute seconds  36 seconds " -- the 's' pass eats the one
               -- inside "minutes". s->m->h is safe because "seconds" holds no
               -- 'm' or 'h', and "minutes" holds no 'h'.
               replace(replace(replace(
                   substring(subtitle from '^[0-9]+[hms][0-9hms.]*'),
                   's', ' seconds '), 'm', ' minutes '), 'h', ' hours ')::interval AS dur
          FROM mem_surface_items
         WHERE surface = 'calls'
           -- Only rows whose subtitle actually opens with a duration token. The
           -- two "I refused instructions" cards are mid-call ALERTS, not call
           -- records (monitor.go alertUnverifiedCommand has no duration in
           -- scope), so they are skipped and the viewer simply shows no time
           -- strip for them. That is correct, not a gap to paper over.
           AND subtitle ~ '^[0-9]+[hms]'
       ) d
 WHERE si.id = d.id
   AND si.surface = 'calls';

COMMIT;
