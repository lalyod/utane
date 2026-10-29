package music

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"mime/multipart"

	"github.com/lalyod/utane/internal/model"
)

func (s *Service) CreateAlbum(
	ctx context.Context,
	artistID string,
	title string,
	year *int,
	coverObject string,
) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		if coverObject != "" {
			s.removeObject(ctx, coverObject)
		}

		return err
	}
	defer tx.Rollback(ctx)

	var albumYear any
	if year != nil {
		albumYear = *year
	}

	var cover any
	if coverObject != "" {
		cover = coverObject
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO albums (artist_id, title, year, cover_object)
		VALUES (?1, ?2, ?3, ?4)
	`, artistID, title, albumYear, cover)
	if err != nil && coverObject != "" {
		s.removeObject(ctx, coverObject)
	}
	if err != nil {
		return serviceError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		// A failed commit can be ambiguous; keep the object rather than break a committed row.
		return err
	}

	return nil
}

func (s *Service) PatchAlbum(
	ctx context.Context,
	id string,
	title string,
	year *int,
	newCover string,
	removeCover bool,
) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		if newCover != "" {
			s.removeObject(ctx, newCover)
		}

		return err
	}
	defer tx.Rollback(ctx)

	var oldCover sql.NullString
	err = tx.QueryRow(ctx, `SELECT cover_object FROM albums WHERE id = ?1`, id).Scan(&oldCover)
	if err != nil {
		if newCover != "" {
			s.removeObject(ctx, newCover)
		}

		return serviceError(err)
	}

	var albumYear any
	if year != nil {
		albumYear = *year
	}
	var cover any
	switch {
	case newCover != "":
		cover = newCover
	case !removeCover && oldCover.Valid:
		cover = oldCover.String
	}

	_, err = tx.Exec(ctx, `UPDATE albums SET title = ?1, year = ?2, cover_object = ?3 WHERE id = ?4`, title, albumYear, cover, id)
	if err != nil {
		if newCover != "" {
			s.removeObject(ctx, newCover)
		}

		return serviceError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		// A failed commit can be ambiguous; keep both objects rather than break a committed row.
		return err
	}

	if oldCover.Valid && oldCover.String != newCover && (newCover != "" || removeCover) {
		s.removeObject(ctx, oldCover.String)
	}

	return nil
}

func (s *Service) DeleteAlbum(ctx context.Context, id string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var cover sql.NullString
	err = tx.QueryRow(ctx, `SELECT cover_object FROM albums WHERE id = ?1`, id).Scan(&cover)
	if err != nil {
		return serviceError(err)
	}

	if _, err = tx.Exec(ctx, `UPDATE tracks SET album_id = NULL, track_number = NULL WHERE album_id = ?1`, id); err != nil {
		return serviceError(err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM albums WHERE id = ?1`, id); err != nil {
		return serviceError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}

	if cover.Valid {
		s.removeObject(ctx, cover.String)
	}

	return nil
}

func (s *Service) GetAlbums(ctx context.Context) ([]model.Album, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT albums.id, albums.artist_id, artists.name, albums.title,
			albums.cover_object IS NOT NULL, COALESCE(albums.year, 0), albums.created_at
		FROM albums
		JOIN artists ON artists.id = albums.artist_id
		ORDER BY artists.name ASC, albums.year ASC NULLS LAST, albums.title ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	albums := []model.Album{}

	for rows.Next() {
		var album model.Album
		if err := rows.Scan(
			&album.ID,
			&album.ArtistID,
			&album.ArtistName,
			&album.Title,
			&album.HasCover,
			&album.Year,
			&album.CreatedAt,
		); err != nil {
			return nil, err
		}
		albums = append(albums, album)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return albums, nil
}

func (s *Service) SearchAlbumCovers(ctx context.Context, id string) ([]CoverCandidate, error) {
	var title string
	var year string
	var artist string
	err := s.DB.QueryRow(ctx, `
		SELECT albums.title, COALESCE(CAST(albums.year AS TEXT), ''), artists.name
		FROM albums
		JOIN artists ON artists.id = albums.artist_id
		WHERE albums.id = ?1
	`, id).Scan(&title, &year, &artist)
	if err != nil {
		return nil, serviceError(err)
	}

	if err := s.waitForMusicBrainz(ctx); err != nil {
		return nil, err
	}

	candidates, err := searchCovers(ctx, s.HTTPClient, s.MusicBrainzEndpoint, s.CoverArtEndpoint, title, year, artist)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUpstream, err)
	}

	return candidates, nil
}

func (s *Service) FetchAlbumCover(ctx context.Context, albumID string, releaseID string) error {
	var albumExists bool
	if err := s.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM albums WHERE id = ?1)`, albumID).Scan(&albumExists); err != nil {
		return serviceError(err)
	}
	if !albumExists {
		return ErrNotFound
	}

	data, contentType, extension, err := downloadCover(ctx, s.HTTPClient, s.CoverArtEndpoint, releaseID)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUpstream, err)
	}

	header := &multipart.FileHeader{
		Filename: "cover" + extension,
		Header:   map[string][]string{"Content-Type": {contentType}},
		Size:     int64(len(data)),
	}
	object, err := s.UploadFile(ctx, bytes.NewReader(data), header, "covers")
	if err != nil {
		return err
	}

	return s.ReplaceAlbumCover(ctx, albumID, object)
}

func (s *Service) ReplaceAlbumCover(ctx context.Context, id string, newCover string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		s.removeObject(ctx, newCover)

		return err
	}
	defer tx.Rollback(ctx)

	var oldCover sql.NullString
	err = tx.QueryRow(ctx, `SELECT cover_object FROM albums WHERE id = ?1`, id).Scan(&oldCover)
	if err != nil {
		s.removeObject(ctx, newCover)

		return serviceError(err)
	}

	if _, err = tx.Exec(ctx, `UPDATE albums SET cover_object = ?1 WHERE id = ?2`, newCover, id); err != nil {
		s.removeObject(ctx, newCover)

		return serviceError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		// A failed commit can be ambiguous; keep both objects rather than break a committed row.
		return err
	}

	if oldCover.Valid && oldCover.String != newCover {
		s.removeObject(ctx, oldCover.String)
	}

	return nil
}

func (s *Service) GetAlbumCoverObject(ctx context.Context, id string) (string, error) {
	var object string
	err := s.DB.QueryRow(ctx, `
		SELECT cover_object
		FROM albums
		WHERE id = ?1 AND cover_object IS NOT NULL
	`, id).Scan(&object)

	return object, err
}
