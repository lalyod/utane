package music

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lalyod/utane/internal/model"
)

func (s *Service) CreateArtist(
	ctx context.Context,
	name string,
) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO artists ( name ) VALUES ( ?1 )`, name)

	return serviceError(err)
}

func (s *Service) GetArtists(ctx context.Context) ([]model.Artist, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT id, name, avatar_object IS NOT NULL, created_at
		FROM artists
		ORDER BY name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	artists := []model.Artist{}

	for rows.Next() {
		var artist model.Artist
		if err := rows.Scan(
			&artist.ID,
			&artist.Name,
			&artist.HasAvatar,
			&artist.CreatedAt,
		); err != nil {
			return nil, err
		}
		artists = append(artists, artist)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return artists, nil
}

func (s *Service) GetArtist(ctx context.Context, id string) (*model.Artist, error) {
	artist := model.Artist{}
	err := s.DB.QueryRow(ctx, `
		SELECT id, name, avatar_object IS NOT NULL, created_at
		FROM artists
		WHERE id = ?1
	`, id).Scan(
		&artist.ID,
		&artist.Name,
		&artist.HasAvatar,
		&artist.CreatedAt,
	)
	if err != nil {
		return nil, serviceError(err)
	}

	return &artist, nil
}

func (s *Service) PatchArtist(ctx context.Context, id string, name string) error {
	result, err := s.DB.Exec(ctx, `
		UPDATE artists
		SET name = ?1
		WHERE id = ?2
		`, name, id)
	if err != nil {
		return serviceError(err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}

	return nil
}

func (s *Service) DeleteArtist(ctx context.Context, id string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var avatar sql.NullString
	err = tx.QueryRow(ctx, `SELECT avatar_object FROM artists WHERE id = ?1`, id).Scan(&avatar)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return serviceError(err)
	}

	var hasMusic bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM albums WHERE artist_id = ?1)
			OR EXISTS (SELECT 1 FROM tracks WHERE artist_id = ?1)
	`, id).Scan(&hasMusic)
	if err != nil {
		return serviceError(err)
	}
	if hasMusic {
		return fmt.Errorf("%w: artist has albums or tracks", ErrConflict)
	}

	if _, err = tx.Exec(ctx, `DELETE FROM artists WHERE id = ?1`, id); err != nil {
		return serviceError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	if avatar.Valid {
		s.removeObject(ctx, avatar.String)
	}

	return nil
}
