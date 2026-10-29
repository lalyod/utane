-- ids are v4-shaped UUID text: the handlers validate them with uuid.Parse.
CREATE TABLE artists (
    id TEXT PRIMARY KEY DEFAULT (lower(
        hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' || substr(hex(randomblob(2)), 2) || '-' ||
        substr('89ab', abs(random()) % 4 + 1, 1) || substr(hex(randomblob(2)), 2) || '-' || hex(randomblob(6))
    )),
    name TEXT NOT NULL UNIQUE,
    avatar_object TEXT,
    avatar_source_url TEXT,
    avatar_attribution TEXT,
    avatar_license TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE albums (
    id TEXT PRIMARY KEY DEFAULT (lower(
        hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' || substr(hex(randomblob(2)), 2) || '-' ||
        substr('89ab', abs(random()) % 4 + 1, 1) || substr(hex(randomblob(2)), 2) || '-' || hex(randomblob(6))
    )),
    artist_id TEXT NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    cover_object TEXT,
    year INT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (artist_id, title)
);

CREATE TABLE tracks (
    id TEXT PRIMARY KEY DEFAULT (lower(
        hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' || substr(hex(randomblob(2)), 2) || '-' ||
        substr('89ab', abs(random()) % 4 + 1, 1) || substr(hex(randomblob(2)), 2) || '-' || hex(randomblob(6))
    )),
    artist_id TEXT NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    album_id TEXT REFERENCES albums(id) ON DELETE SET NULL,

    title TEXT NOT NULL,
    duration INT NOT NULL, -- seconds
    object_path TEXT NOT NULL, -- file path under the media directory

    track_number INT,
    disc_number INT DEFAULT 1,
    synced_lyrics TEXT NOT NULL DEFAULT '',
    cover_object TEXT,

    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_tracks_artist ON tracks(artist_id);
CREATE INDEX idx_tracks_album ON tracks(album_id);

CREATE TABLE playlists (
    id TEXT PRIMARY KEY DEFAULT (lower(
        hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' || substr(hex(randomblob(2)), 2) || '-' ||
        substr('89ab', abs(random()) % 4 + 1, 1) || substr(hex(randomblob(2)), 2) || '-' || hex(randomblob(6))
    )),
    name TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE playlist_tracks (
    playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
    track_id TEXT NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    position INT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (playlist_id, track_id),
    UNIQUE (playlist_id, position)
);

CREATE INDEX idx_playlist_tracks_track ON playlist_tracks(track_id);
