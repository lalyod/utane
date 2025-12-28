-- Drop indexes
DROP INDEX IF EXISTS idx_tracks_album;
DROP INDEX IF EXISTS idx_tracks_artist;

-- Drop tables (child → parent)
DROP TABLE IF EXISTS tracks;
DROP TABLE IF EXISTS albums;
DROP TABLE IF EXISTS artists;

-- Optional: remove extension
DROP EXTENSION IF EXISTS "uuid-ossp";

