-- 003_search.sql — Full-text search configuration with project synonym dictionary.
-- The synonym source file lives at db/synonyms.syn and must be deployed to
-- $PGDATA/tsearch_data/infinity_synonyms.syn for the dictionary to load.
--
-- For environments where you can't drop a file in tsearch_data (e.g. managed
-- Postgres without superuser FS access), use the simple-language config below
-- by setting INFINITY_SIMPLE_FTS=true at deploy time and skipping this migration.

DO $$
BEGIN
    -- Only define the synonym dictionary if synonym files are accessible.
    BEGIN
        EXECUTE $sql$
            CREATE TEXT SEARCH DICTIONARY infinity_synonyms (
                TEMPLATE = synonym,
                SYNONYMS = infinity_synonyms
            );
        $sql$;
    EXCEPTION WHEN others THEN
        RAISE NOTICE 'infinity_synonyms dictionary not created (% — falling back to plain english config)', SQLERRM;
    END;
END$$;

-- Always create the search config; fall back to english_stem alone if synonyms failed.
DO $$
BEGIN
    BEGIN
        EXECUTE 'CREATE TEXT SEARCH CONFIGURATION infinity_search (COPY = english)';
    EXCEPTION WHEN duplicate_object THEN
        NULL;
    END;
END$$;

DO $$
BEGIN
    BEGIN
        EXECUTE $sql$
            ALTER TEXT SEARCH CONFIGURATION infinity_search
                ALTER MAPPING FOR asciiword, asciihword, hword_asciipart, word, hword, hword_part
                WITH infinity_synonyms, english_stem
        $sql$;
    EXCEPTION WHEN others THEN
        RAISE NOTICE 'infinity_search alter mapping fell back to english_stem only (%)', SQLERRM;
    END;
END$$;
