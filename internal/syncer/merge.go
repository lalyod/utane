package syncer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/lalyod/utane/internal/db"
	"github.com/lalyod/utane/internal/media"
)

// Merger applies peer snapshots to the local database and media store.
type Merger struct {
	DB    *db.DB
	Media *media.Store
}

var tombstoneTables = map[string]string{
	"artist":   "artists",
	"album":    "albums",
	"track":    "tracks",
	"playlist": "playlists",
}

type localState struct {
	updated map[string]int64 // "kind/id" -> updated_at
	tombs   map[string]int64 // "kind/id" -> deleted_at
}

func key(kind string, id string) string { return kind + "/" + id }

func (m *Merger) loadState(ctx context.Context) (*localState, error) {
	state := &localState{updated: map[string]int64{}, tombs: map[string]int64{}}
	for kind, table := range tombstoneTables {
		rows, err := m.DB.Query(ctx, `SELECT id, updated_at FROM `+table)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var at int64
			if err := rows.Scan(&id, &at); err != nil {
				rows.Close()
				return nil, err
			}
			state.updated[key(kind, id)] = at
		}
		if err := closeRows(rows); err != nil {
			return nil, err
		}
	}

	rows, err := m.DB.Query(ctx, `SELECT kind, id, deleted_at FROM tombstones`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kind, id string
		var at int64
		if err := rows.Scan(&kind, &id, &at); err != nil {
			rows.Close()
			return nil, err
		}
		state.tombs[key(kind, id)] = at
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}

	return state, nil
}

// wanted reports whether a remote row is new or newer than what we have, and not deleted since.
func (s *localState) wanted(kind string, id string, updatedAt int64) bool {
	if at, ok := s.updated[key(kind, id)]; ok && at >= updatedAt {
		return false
	}
	if at, ok := s.tombs[key(kind, id)]; ok && at >= updatedAt {
		return false
	}

	return true
}

// ensure copies a media file from the peer unless we already have it.
func (m *Merger) ensure(ctx context.Context, src Source, name string) error {
	if name == "" || strings.Contains(name, "..") {
		return fmt.Errorf("invalid media name %q", name)
	}
	if file, err := m.Media.Open(name); err == nil {
		file.Close()

		return nil
	}
	body, err := src.Fetch(ctx, name)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", name, err)
	}
	defer body.Close()

	return m.Media.Put(name, body)
}

func (m *Merger) ensureAll(ctx context.Context, src Source, names ...*string) error {
	for _, name := range names {
		if name == nil || *name == "" {
			continue
		}
		if err := m.ensure(ctx, src, *name); err != nil {
			return err
		}
	}

	return nil
}

// Merge applies the peer's snapshot and returns how many rows changed. A media file that cannot be
// fetched skips only its own row, so a half-synced folder catches up on a later round; those
// errors are returned together with the count.
func (m *Merger) Merge(ctx context.Context, src Source) (int, error) {
	snap, err := src.Snapshot(ctx)
	if err != nil {
		return 0, err
	}
	if err := snap.validate(); err != nil {
		return 0, err
	}
	state, err := m.loadState(ctx)
	if err != nil {
		return 0, err
	}
	before, err := m.referencedMedia(ctx)
	if err != nil {
		return 0, err
	}

	// Fetch media before the write transaction so a slow peer never holds the database lock.
	var problems []error
	artists := snap.Artists[:0:0]
	for _, a := range snap.Artists {
		if !state.wanted("artist", a.ID, a.UpdatedAt) {
			continue
		}
		if err := m.ensureAll(ctx, src, a.AvatarObject); err != nil {
			problems = append(problems, err)
			continue
		}
		artists = append(artists, a)
	}
	albums := snap.Albums[:0:0]
	for _, a := range snap.Albums {
		if !state.wanted("album", a.ID, a.UpdatedAt) {
			continue
		}
		if err := m.ensureAll(ctx, src, a.CoverObject); err != nil {
			problems = append(problems, err)
			continue
		}
		albums = append(albums, a)
	}
	tracks := snap.Tracks[:0:0]
	for _, t := range snap.Tracks {
		if !state.wanted("track", t.ID, t.UpdatedAt) {
			continue
		}
		if err := m.ensureAll(ctx, src, &t.ObjectPath, t.CoverObject); err != nil {
			problems = append(problems, err)
			continue
		}
		tracks = append(tracks, t)
	}
	playlists := snap.Playlists[:0:0]
	for _, p := range snap.Playlists {
		if state.wanted("playlist", p.ID, p.UpdatedAt) {
			playlists = append(playlists, p)
		}
	}

	changed, err := m.apply(ctx, snap.Tombstones, artists, albums, tracks, playlists, snap.Plays)
	if err != nil {
		return 0, err
	}

	// Files no row refers to any more (rows deleted or replaced by the merge) go away.
	after, err := m.referencedMedia(ctx)
	if err != nil {
		return changed, errors.Join(problems...)
	}
	for name := range before {
		if !after[name] {
			if err := m.Media.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
				problems = append(problems, err)
			}
		}
	}

	return changed, errors.Join(problems...)
}

func (m *Merger) apply(ctx context.Context, tombstones []Tombstone, artists []Artist, albums []Album,
	tracks []Track, playlists []Playlist, plays []Play) (int, error) {
	tx, err := m.DB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	changed := 0
	count := func(result db.Result) {
		changed += int(result.RowsAffected())
	}

	for _, t := range tombstones {
		table, ok := tombstoneTables[t.Kind]
		if !ok {
			continue
		}
		var updatedAt int64
		err := tx.QueryRow(ctx, `SELECT updated_at FROM `+table+` WHERE id = ?1`, t.ID).Scan(&updatedAt)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := tx.Exec(ctx, `
				INSERT OR IGNORE INTO tombstones (kind, id, deleted_at) VALUES (?1, ?2, ?3)`,
				t.Kind, t.ID, t.DeletedAt); err != nil {
				return 0, err
			}
		case err != nil:
			return 0, err
		case updatedAt < t.DeletedAt:
			result, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE id = ?1`, t.ID)
			if err != nil {
				return 0, err
			}
			count(result)
		}
	}

	// tombstoned reports a local delete at least as new as the remote row.
	tombstoned := func(kind string, id string, updatedAt int64) (bool, error) {
		var deletedAt int64
		err := tx.QueryRow(ctx, `SELECT deleted_at FROM tombstones WHERE kind = ?1 AND id = ?2`, kind, id).Scan(&deletedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}

		return deletedAt >= updatedAt, err
	}
	// resolve returns the local id for a remote id: itself if the row exists, else the mapped one.
	resolve := func(table string, mapped map[string]string, id string) (string, error) {
		if local, ok := mapped[id]; ok {
			return local, nil
		}
		var found string
		err := tx.QueryRow(ctx, `SELECT id FROM `+table+` WHERE id = ?1`, id).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}

		return found, err
	}
	clearTombstone := func(kind string, id string) error {
		_, err := tx.Exec(ctx, `DELETE FROM tombstones WHERE kind = ?1 AND id = ?2`, kind, id)

		return err
	}

	artistMap := map[string]string{}
	for _, a := range artists {
		if dead, err := tombstoned("artist", a.ID, a.UpdatedAt); err != nil {
			return 0, err
		} else if dead {
			continue
		}
		local, err := resolve("artists", nil, a.ID)
		if err != nil {
			return 0, err
		}
		if local == "" {
			// Same artist created on two devices: keep the local row.
			err := tx.QueryRow(ctx, `SELECT id FROM artists WHERE name = ?1`, a.Name).Scan(&local)
			if err == nil {
				artistMap[a.ID] = local

				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return 0, err
			}
		} else if taken, err := nameTaken(ctx, tx, `SELECT EXISTS (SELECT 1 FROM artists WHERE name = ?1 AND id <> ?2)`, a.Name, a.ID); err != nil {
			return 0, err
		} else if taken {
			continue
		}
		result, err := tx.Exec(ctx, `
			INSERT INTO artists (id, name, avatar_object, avatar_source_url, avatar_attribution, avatar_license, created_at, updated_at)
			VALUES (?1, ?2, ?3, ?4, ?5, ?6, COALESCE(NULLIF(?7, ''), CURRENT_TIMESTAMP), ?8)
			ON CONFLICT (id) DO UPDATE SET name = excluded.name, avatar_object = excluded.avatar_object,
				avatar_source_url = excluded.avatar_source_url, avatar_attribution = excluded.avatar_attribution,
				avatar_license = excluded.avatar_license, updated_at = excluded.updated_at
			WHERE excluded.updated_at > artists.updated_at`,
			a.ID, a.Name, a.AvatarObject, a.AvatarSourceURL, a.AvatarAttribution, a.AvatarLicense, a.CreatedAt, a.UpdatedAt)
		if err != nil {
			return 0, err
		}
		artistMap[a.ID] = a.ID
		if result.RowsAffected() > 0 {
			count(result)
			if err := clearTombstone("artist", a.ID); err != nil {
				return 0, err
			}
		}
	}

	albumMap := map[string]string{}
	for _, a := range albums {
		if dead, err := tombstoned("album", a.ID, a.UpdatedAt); err != nil {
			return 0, err
		} else if dead {
			continue
		}
		artistID, err := resolve("artists", artistMap, a.ArtistID)
		if err != nil {
			return 0, err
		}
		if artistID == "" {
			continue // artist not here yet; retried next round
		}
		local, err := resolve("albums", nil, a.ID)
		if err != nil {
			return 0, err
		}
		if local == "" {
			err := tx.QueryRow(ctx, `SELECT id FROM albums WHERE artist_id = ?1 AND title = ?2`, artistID, a.Title).Scan(&local)
			if err == nil {
				albumMap[a.ID] = local

				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return 0, err
			}
		} else if taken, err := nameTaken(ctx, tx,
			`SELECT EXISTS (SELECT 1 FROM albums WHERE artist_id = ?1 AND title = ?2 AND id <> ?3)`, artistID, a.Title, a.ID); err != nil {
			return 0, err
		} else if taken {
			continue
		}
		result, err := tx.Exec(ctx, `
			INSERT INTO albums (id, artist_id, title, cover_object, year, created_at, updated_at)
			VALUES (?1, ?2, ?3, ?4, ?5, COALESCE(NULLIF(?6, ''), CURRENT_TIMESTAMP), ?7)
			ON CONFLICT (id) DO UPDATE SET artist_id = excluded.artist_id, title = excluded.title,
				cover_object = excluded.cover_object, year = excluded.year, updated_at = excluded.updated_at
			WHERE excluded.updated_at > albums.updated_at`,
			a.ID, artistID, a.Title, a.CoverObject, a.Year, a.CreatedAt, a.UpdatedAt)
		if err != nil {
			return 0, err
		}
		albumMap[a.ID] = a.ID
		if result.RowsAffected() > 0 {
			count(result)
			if err := clearTombstone("album", a.ID); err != nil {
				return 0, err
			}
		}
	}

	for _, t := range tracks {
		if dead, err := tombstoned("track", t.ID, t.UpdatedAt); err != nil {
			return 0, err
		} else if dead {
			continue
		}
		artistID, err := resolve("artists", artistMap, t.ArtistID)
		if err != nil {
			return 0, err
		}
		if artistID == "" {
			continue
		}
		var albumID *string
		if t.AlbumID != nil {
			local, err := resolve("albums", albumMap, *t.AlbumID)
			if err != nil {
				return 0, err
			}
			if local == "" {
				continue // album not here yet; retried next round
			}
			albumID = &local
		}
		result, err := tx.Exec(ctx, `
			INSERT INTO tracks (id, artist_id, album_id, title, duration, object_path, track_number, disc_number,
				synced_lyrics, cover_object, created_at, updated_at)
			VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, COALESCE(NULLIF(?11, ''), CURRENT_TIMESTAMP), ?12)
			ON CONFLICT (id) DO UPDATE SET artist_id = excluded.artist_id, album_id = excluded.album_id,
				title = excluded.title, duration = excluded.duration, object_path = excluded.object_path,
				track_number = excluded.track_number, disc_number = excluded.disc_number,
				synced_lyrics = excluded.synced_lyrics, cover_object = excluded.cover_object,
				updated_at = excluded.updated_at
			WHERE excluded.updated_at > tracks.updated_at`,
			t.ID, artistID, albumID, t.Title, t.Duration, t.ObjectPath, t.TrackNumber, t.DiscNumber,
			t.SyncedLyrics, t.CoverObject, t.CreatedAt, t.UpdatedAt)
		if err != nil {
			return 0, err
		}
		if result.RowsAffected() > 0 {
			count(result)
			if err := clearTombstone("track", t.ID); err != nil {
				return 0, err
			}
		}
	}

	for _, p := range playlists {
		if dead, err := tombstoned("playlist", p.ID, p.UpdatedAt); err != nil {
			return 0, err
		} else if dead {
			continue
		}
		// Every member must exist (or be deleted) first, or the playlist would be applied with holes
		// and never repaired, because its timestamp would already match.
		members := make([]string, 0, len(p.TrackIDs))
		complete := true
		for _, trackID := range p.TrackIDs {
			local, err := resolve("tracks", nil, trackID)
			if err != nil {
				return 0, err
			}
			if local != "" {
				members = append(members, local)
			} else if dead, err := tombstoned("track", trackID, 0); err != nil {
				return 0, err
			} else if !dead {
				complete = false
				break
			}
		}
		if !complete {
			continue
		}

		local, err := resolve("playlists", nil, p.ID)
		if err != nil {
			return 0, err
		}
		if local == "" {
			err := tx.QueryRow(ctx, `SELECT id FROM playlists WHERE name = ?1`, p.Name).Scan(&local)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return 0, err
			}
		} else if taken, err := nameTaken(ctx, tx, `SELECT EXISTS (SELECT 1 FROM playlists WHERE name = ?1 AND id <> ?2)`, p.Name, p.ID); err != nil {
			return 0, err
		} else if taken {
			continue
		}

		if local == "" {
			local = p.ID
			if _, err := tx.Exec(ctx, `
				INSERT INTO playlists (id, name, created_at, updated_at)
				VALUES (?1, ?2, COALESCE(NULLIF(?3, ''), CURRENT_TIMESTAMP), ?4)`,
				p.ID, p.Name, p.CreatedAt, p.UpdatedAt); err != nil {
				return 0, err
			}
		} else {
			var localUpdated int64
			if err := tx.QueryRow(ctx, `SELECT updated_at FROM playlists WHERE id = ?1`, local).Scan(&localUpdated); err != nil {
				return 0, err
			}
			if localUpdated >= p.UpdatedAt {
				continue
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM playlist_tracks WHERE playlist_id = ?1`, local); err != nil {
			return 0, err
		}
		for position, trackID := range members {
			if _, err := tx.Exec(ctx, `
				INSERT INTO playlist_tracks (playlist_id, track_id, position)
				VALUES (?1, ?2, ?3) ON CONFLICT (playlist_id, track_id) DO NOTHING`,
				local, trackID, position+1); err != nil {
				return 0, err
			}
		}
		// Last, because the membership writes above bump updated_at to now.
		result, err := tx.Exec(ctx, `UPDATE playlists SET name = ?1, updated_at = ?2 WHERE id = ?3`, p.Name, p.UpdatedAt, local)
		if err != nil {
			return 0, err
		}
		count(result)
		if err := clearTombstone("playlist", local); err != nil {
			return 0, err
		}
	}

	// Plays come last so their tracks exist; the longer listen wins and a play of a deleted track is dropped.
	for _, p := range plays {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tracks WHERE id = ?1)`, p.TrackID).Scan(&exists); err != nil {
			return 0, err
		}
		if !exists {
			continue
		}
		result, err := tx.Exec(ctx, `
			INSERT INTO plays (id, track_id, played_at, seconds, updated_at) VALUES (?1, ?2, ?3, ?4, ?5)
			ON CONFLICT (id) DO UPDATE SET seconds = excluded.seconds, updated_at = excluded.updated_at
			WHERE excluded.seconds > plays.seconds AND plays.track_id = excluded.track_id`,
			p.ID, p.TrackID, p.PlayedAt, p.Seconds, p.UpdatedAt)
		if err != nil {
			return 0, err
		}
		count(result)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}

	return changed, nil
}

func nameTaken(ctx context.Context, tx *db.Tx, query string, args ...any) (bool, error) {
	var taken bool
	err := tx.QueryRow(ctx, query, args...).Scan(&taken)

	return taken, err
}

// referencedMedia is every store name some row points at.
func (m *Merger) referencedMedia(ctx context.Context) (map[string]bool, error) {
	rows, err := m.DB.Query(ctx, `
		SELECT avatar_object FROM artists WHERE avatar_object IS NOT NULL
		UNION SELECT cover_object FROM albums WHERE cover_object IS NOT NULL
		UNION SELECT object_path FROM tracks
		UNION SELECT cover_object FROM tracks WHERE cover_object IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		names[name] = true
	}

	return names, closeRows(rows)
}
