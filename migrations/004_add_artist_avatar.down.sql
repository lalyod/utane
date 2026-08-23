ALTER TABLE IF EXISTS artists
    DROP COLUMN IF EXISTS avatar_license,
    DROP COLUMN IF EXISTS avatar_attribution,
    DROP COLUMN IF EXISTS avatar_source_url,
    DROP COLUMN IF EXISTS avatar_object;
