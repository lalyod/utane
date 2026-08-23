ALTER TABLE IF EXISTS artists
    ADD COLUMN IF NOT EXISTS avatar_object TEXT,
    ADD COLUMN IF NOT EXISTS avatar_source_url TEXT,
    ADD COLUMN IF NOT EXISTS avatar_attribution TEXT,
    ADD COLUMN IF NOT EXISTS avatar_license TEXT;
