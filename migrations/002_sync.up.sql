-- Change tracking for device sync. updated_at is unix milliseconds; triggers keep it current so no
-- handler has to know about sync. A sync merge writes its own updated_at, which the update triggers keep.
ALTER TABLE artists ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE albums ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tracks ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE playlists ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0;

UPDATE artists SET updated_at = CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER);
UPDATE albums SET updated_at = CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER);
UPDATE tracks SET updated_at = CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER);
UPDATE playlists SET updated_at = CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER);

CREATE TABLE tombstones (
    kind TEXT NOT NULL,
    id TEXT NOT NULL,
    deleted_at INTEGER NOT NULL,
    PRIMARY KEY (kind, id)
);

CREATE TRIGGER artists_sync_insert AFTER INSERT ON artists WHEN NEW.updated_at = 0 BEGIN
    UPDATE artists SET updated_at = CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER) WHERE id = NEW.id;
END;
CREATE TRIGGER artists_sync_update AFTER UPDATE ON artists WHEN NEW.updated_at = OLD.updated_at BEGIN
    UPDATE artists SET updated_at = CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER) WHERE id = NEW.id;
END;
CREATE TRIGGER artists_sync_delete AFTER DELETE ON artists BEGIN
    INSERT OR REPLACE INTO tombstones (kind, id, deleted_at)
    VALUES ('artist', OLD.id, CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER));
END;

CREATE TRIGGER albums_sync_insert AFTER INSERT ON albums WHEN NEW.updated_at = 0 BEGIN
    UPDATE albums SET updated_at = CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER) WHERE id = NEW.id;
END;
CREATE TRIGGER albums_sync_update AFTER UPDATE ON albums WHEN NEW.updated_at = OLD.updated_at BEGIN
    UPDATE albums SET updated_at = CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER) WHERE id = NEW.id;
END;
CREATE TRIGGER albums_sync_delete AFTER DELETE ON albums BEGIN
    INSERT OR REPLACE INTO tombstones (kind, id, deleted_at)
    VALUES ('album', OLD.id, CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER));
END;

CREATE TRIGGER tracks_sync_insert AFTER INSERT ON tracks WHEN NEW.updated_at = 0 BEGIN
    UPDATE tracks SET updated_at = CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER) WHERE id = NEW.id;
END;
CREATE TRIGGER tracks_sync_update AFTER UPDATE ON tracks WHEN NEW.updated_at = OLD.updated_at BEGIN
    UPDATE tracks SET updated_at = CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER) WHERE id = NEW.id;
END;
CREATE TRIGGER tracks_sync_delete AFTER DELETE ON tracks BEGIN
    INSERT OR REPLACE INTO tombstones (kind, id, deleted_at)
    VALUES ('track', OLD.id, CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER));
END;

CREATE TRIGGER playlists_sync_insert AFTER INSERT ON playlists WHEN NEW.updated_at = 0 BEGIN
    UPDATE playlists SET updated_at = CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER) WHERE id = NEW.id;
END;
CREATE TRIGGER playlists_sync_update AFTER UPDATE ON playlists WHEN NEW.updated_at = OLD.updated_at BEGIN
    UPDATE playlists SET updated_at = CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER) WHERE id = NEW.id;
END;
CREATE TRIGGER playlists_sync_delete AFTER DELETE ON playlists BEGIN
    INSERT OR REPLACE INTO tombstones (kind, id, deleted_at)
    VALUES ('playlist', OLD.id, CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER));
END;

-- A playlist syncs as one unit, so any membership change bumps the playlist.
CREATE TRIGGER playlist_tracks_sync_insert AFTER INSERT ON playlist_tracks BEGIN
    UPDATE playlists SET updated_at = CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER) WHERE id = NEW.playlist_id;
END;
CREATE TRIGGER playlist_tracks_sync_update AFTER UPDATE ON playlist_tracks BEGIN
    UPDATE playlists SET updated_at = CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER) WHERE id = NEW.playlist_id;
END;
CREATE TRIGGER playlist_tracks_sync_delete AFTER DELETE ON playlist_tracks BEGIN
    UPDATE playlists SET updated_at = CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER) WHERE id = OLD.playlist_id;
END;
