// Package syncer merges the library of this device with the libraries of other devices.
//
// Every device exposes a Snapshot of its whole library. Merging a peer snapshot is last-writer-wins
// per row by updated_at; deletes travel as tombstones. Where the snapshot comes from (a shared
// folder, a LAN peer) is a Source.
package syncer

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/lalyod/utane/internal/db"
	"github.com/lalyod/utane/internal/music"
)

type Artist struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	AvatarObject      *string `json:"avatarObject"`
	AvatarSourceURL   *string `json:"avatarSourceUrl"`
	AvatarAttribution *string `json:"avatarAttribution"`
	AvatarLicense     *string `json:"avatarLicense"`
	CreatedAt         string  `json:"createdAt"`
	UpdatedAt         int64   `json:"updatedAt"`
}

type Album struct {
	ID          string  `json:"id"`
	ArtistID    string  `json:"artistId"`
	Title       string  `json:"title"`
	CoverObject *string `json:"coverObject"`
	Year        *int    `json:"year"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   int64   `json:"updatedAt"`
}

type Track struct {
	ID           string  `json:"id"`
	ArtistID     string  `json:"artistId"`
	AlbumID      *string `json:"albumId"`
	Title        string  `json:"title"`
	Duration     int     `json:"duration"`
	ObjectPath   string  `json:"objectPath"`
	TrackNumber  *int    `json:"trackNumber"`
	DiscNumber   *int    `json:"discNumber"`
	SyncedLyrics string  `json:"syncedLyrics"`
	CoverObject  *string `json:"coverObject"`
	CreatedAt    string  `json:"createdAt"`
	UpdatedAt    int64   `json:"updatedAt"`
}

type Playlist struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	TrackIDs  []string `json:"trackIds"` // in position order
	CreatedAt string   `json:"createdAt"`
	UpdatedAt int64    `json:"updatedAt"`
}

// Play is one listen. Plays are append-only: the longer listen wins, nothing is ever deleted
// (a deleted track takes its plays with it through the foreign key).
type Play struct {
	ID        string `json:"id"`
	TrackID   string `json:"trackId"`
	PlayedAt  int64  `json:"playedAt"`
	Seconds   int    `json:"seconds"`
	UpdatedAt int64  `json:"updatedAt"`
}

type Tombstone struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	DeletedAt int64  `json:"deletedAt"`
}

type Snapshot struct {
	Device     string      `json:"device"`
	At         int64       `json:"at"`
	Artists    []Artist    `json:"artists"`
	Albums     []Album     `json:"albums"`
	Tracks     []Track     `json:"tracks"`
	Playlists  []Playlist  `json:"playlists"`
	Plays      []Play      `json:"plays"` // only the stats window; absent in snapshots from older versions
	Tombstones []Tombstone `json:"tombstones"`
}

// Source is where a peer's snapshot and media files come from.
type Source interface {
	Snapshot(ctx context.Context) (*Snapshot, error)
	// Fetch opens a media file by its store name ("tracks/<uuid>.mp3").
	Fetch(ctx context.Context, name string) (io.ReadCloser, error)
}

// Export reads the whole local library.
func Export(ctx context.Context, conn *db.DB, device string) (*Snapshot, error) {
	snap := &Snapshot{
		Device:     device,
		Artists:    []Artist{},
		Albums:     []Album{},
		Tracks:     []Track{},
		Playlists:  []Playlist{},
		Plays:      []Play{},
		Tombstones: []Tombstone{},
	}

	rows, err := conn.Query(ctx, `
		SELECT id, name, avatar_object, avatar_source_url, avatar_attribution, avatar_license,
			CAST(created_at AS TEXT), updated_at
		FROM artists ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a Artist
		var created sql.NullString
		if err := rows.Scan(&a.ID, &a.Name, &a.AvatarObject, &a.AvatarSourceURL, &a.AvatarAttribution,
			&a.AvatarLicense, &created, &a.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		a.CreatedAt = created.String
		snap.Artists = append(snap.Artists, a)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}

	rows, err = conn.Query(ctx, `
		SELECT id, artist_id, title, cover_object, year, CAST(created_at AS TEXT), updated_at
		FROM albums ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a Album
		var created sql.NullString
		if err := rows.Scan(&a.ID, &a.ArtistID, &a.Title, &a.CoverObject, &a.Year, &created, &a.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		a.CreatedAt = created.String
		snap.Albums = append(snap.Albums, a)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}

	rows, err = conn.Query(ctx, `
		SELECT id, artist_id, album_id, title, duration, object_path, track_number, disc_number,
			synced_lyrics, cover_object, CAST(created_at AS TEXT), updated_at
		FROM tracks ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t Track
		var created sql.NullString
		if err := rows.Scan(&t.ID, &t.ArtistID, &t.AlbumID, &t.Title, &t.Duration, &t.ObjectPath, &t.TrackNumber,
			&t.DiscNumber, &t.SyncedLyrics, &t.CoverObject, &created, &t.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		t.CreatedAt = created.String
		snap.Tracks = append(snap.Tracks, t)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}

	rows, err = conn.Query(ctx, `
		SELECT id, name, CAST(created_at AS TEXT), updated_at,
			COALESCE((SELECT group_concat(track_id, ',') FROM (
				SELECT track_id FROM playlist_tracks WHERE playlist_id = playlists.id ORDER BY position)), '')
		FROM playlists ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p Playlist
		var created sql.NullString
		var ids string
		if err := rows.Scan(&p.ID, &p.Name, &created, &p.UpdatedAt, &ids); err != nil {
			rows.Close()
			return nil, err
		}
		p.CreatedAt = created.String
		p.TrackIDs = splitIDs(ids)
		snap.Playlists = append(snap.Playlists, p)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}

	rows, err = conn.Query(ctx, `
		SELECT id, track_id, played_at, seconds, updated_at FROM plays WHERE played_at >= ?1 ORDER BY id`,
		time.Now().Add(-music.StatsWindow).UnixMilli())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p Play
		if err := rows.Scan(&p.ID, &p.TrackID, &p.PlayedAt, &p.Seconds, &p.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		snap.Plays = append(snap.Plays, p)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}

	rows, err = conn.Query(ctx, `SELECT kind, id, deleted_at FROM tombstones ORDER BY kind, id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t Tombstone
		if err := rows.Scan(&t.Kind, &t.ID, &t.DeletedAt); err != nil {
			rows.Close()
			return nil, err
		}
		snap.Tombstones = append(snap.Tombstones, t)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}

	// At is the newest change, not the wall clock, so an unchanged library exports identical bytes
	// and the folder transport can skip rewriting its file.
	for _, a := range snap.Artists {
		snap.At = max(snap.At, a.UpdatedAt)
	}
	for _, a := range snap.Albums {
		snap.At = max(snap.At, a.UpdatedAt)
	}
	for _, t := range snap.Tracks {
		snap.At = max(snap.At, t.UpdatedAt)
	}
	for _, p := range snap.Playlists {
		snap.At = max(snap.At, p.UpdatedAt)
	}
	for _, p := range snap.Plays {
		snap.At = max(snap.At, p.UpdatedAt)
	}
	for _, t := range snap.Tombstones {
		snap.At = max(snap.At, t.DeletedAt)
	}

	return snap, nil
}

func closeRows(rows *sql.Rows) error {
	err := rows.Err()
	if cerr := rows.Close(); err == nil {
		err = cerr
	}

	return err
}

func splitIDs(text string) []string {
	ids := []string{}
	start := 0
	for i := 0; i <= len(text); i++ {
		if i == len(text) || text[i] == ',' {
			if i > start {
				ids = append(ids, text[start:i])
			}
			start = i + 1
		}
	}

	return ids
}

// MediaNames lists every store name the snapshot refers to.
func (s *Snapshot) MediaNames() []string {
	seen := map[string]bool{}
	names := []string{}
	add := func(name *string) {
		if name != nil && *name != "" && !seen[*name] {
			seen[*name] = true
			names = append(names, *name)
		}
	}
	for _, a := range s.Artists {
		add(a.AvatarObject)
	}
	for _, a := range s.Albums {
		add(a.CoverObject)
	}
	for _, t := range s.Tracks {
		add(&t.ObjectPath)
		add(t.CoverObject)
	}

	return names
}

// validate rejects snapshots a peer could use to put junk in the library: handlers assume uuid ids.
func (s *Snapshot) validate() error {
	check := func(kind string, id string) error {
		if _, err := uuid.Parse(id); err != nil {
			return fmt.Errorf("snapshot has invalid %s id %q", kind, id)
		}

		return nil
	}
	for _, a := range s.Artists {
		if err := check("artist", a.ID); err != nil {
			return err
		}
	}
	for _, a := range s.Albums {
		if err := check("album", a.ID); err != nil {
			return err
		}
	}
	for _, t := range s.Tracks {
		if err := check("track", t.ID); err != nil {
			return err
		}
	}
	for _, p := range s.Playlists {
		if err := check("playlist", p.ID); err != nil {
			return err
		}
	}
	for _, p := range s.Plays {
		if err := check("play", p.ID); err != nil {
			return err
		}
		if err := check("play track", p.TrackID); err != nil {
			return err
		}
		if p.Seconds < 1 || p.Seconds > 4*3600 {
			return fmt.Errorf("snapshot has invalid play length %d", p.Seconds)
		}
	}
	for _, t := range s.Tombstones {
		if err := check("tombstone", t.ID); err != nil {
			return err
		}
	}

	return nil
}
