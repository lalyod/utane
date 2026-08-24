package music

import (
	"context"

	"github.com/lalyod/utane/internal/model"
)

func (s *Service) GetPlaylists(ctx context.Context) ([]model.Playlist, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT playlists.id, playlists.name, playlists.created_at,
			COALESCE(array_agg(playlist_tracks.track_id::text ORDER BY playlist_tracks.position) FILTER (WHERE playlist_tracks.track_id IS NOT NULL), ARRAY[]::text[])
		FROM playlists
		LEFT JOIN playlist_tracks ON playlist_tracks.playlist_id = playlists.id
		GROUP BY playlists.id
		ORDER BY playlists.name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	playlists := []model.Playlist{}
	for rows.Next() {
		var playlist model.Playlist
		if err := rows.Scan(&playlist.ID, &playlist.Name, &playlist.CreatedAt, &playlist.TrackIDs); err != nil {
			return nil, err
		}
		playlists = append(playlists, playlist)
	}

	return playlists, rows.Err()
}

func (s *Service) CreatePlaylist(ctx context.Context, name string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO playlists (name) VALUES ($1)`, name)

	return serviceError(err)
}

func (s *Service) PatchPlaylist(ctx context.Context, id string, name string) error {
	result, err := s.DB.Exec(ctx, `UPDATE playlists SET name = $1 WHERE id = $2`, name, id)
	if err != nil {
		return serviceError(err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}

	return nil
}

func (s *Service) DeletePlaylist(ctx context.Context, id string) error {
	result, err := s.DB.Exec(ctx, `DELETE FROM playlists WHERE id = $1`, id)
	if err != nil {
		return serviceError(err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}

	return nil
}

func (s *Service) AddPlaylistTrack(ctx context.Context, playlistID string, trackID string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM playlists WHERE id = $1)`, playlistID).Scan(&exists); err != nil {
		return serviceError(err)
	}
	if !exists {
		return ErrNotFound
	}
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tracks WHERE id = $1)`, trackID).Scan(&exists); err != nil {
		return serviceError(err)
	}
	if !exists {
		return ErrInvalid
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO playlist_tracks (playlist_id, track_id, position)
		VALUES ($1, $2, COALESCE((SELECT max(position) + 1 FROM playlist_tracks WHERE playlist_id = $1), 1))
		ON CONFLICT (playlist_id, track_id) DO NOTHING
	`, playlistID, trackID)
	if err != nil {
		return serviceError(err)
	}

	return tx.Commit(ctx)
}

func (s *Service) DeletePlaylistTrack(ctx context.Context, playlistID string, trackID string) error {
	result, err := s.DB.Exec(ctx, `DELETE FROM playlist_tracks WHERE playlist_id = $1 AND track_id = $2`, playlistID, trackID)
	if err != nil {
		return serviceError(err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}

	return nil
}
