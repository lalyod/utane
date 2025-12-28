CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE artists (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP DEFAULT now()
);

CREATE TABLE albums (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    cover_object TEXT,
    year INT,
    created_at TIMESTAMP DEFAULT now(),
    UNIQUE (artist_id, title)
);

CREATE TABLE tracks (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    album_id UUID REFERENCES albums(id) ON DELETE SET NULL,

    title TEXT NOT NULL,
    duration INT NOT NULL, -- seconds
    object_path TEXT NOT NULL, -- MinIO path

    track_number INT,
    disc_number INT DEFAULT 1,

    created_at TIMESTAMP DEFAULT now()
);

CREATE INDEX idx_tracks_artist ON tracks(artist_id);
CREATE INDEX idx_tracks_album ON tracks(album_id);
