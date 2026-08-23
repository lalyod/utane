package music

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"mime/multipart"
)

func readRasterImage(reader io.Reader, contentLength int64) ([]byte, string, string, error) {
	if contentLength > maxCoverSize {
		return nil, "", "", ErrCoverTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxCoverSize+1))
	if err != nil {
		return nil, "", "", err
	}
	if len(data) > maxCoverSize {
		return nil, "", "", ErrCoverTooLarge
	}

	contentType, extension, ok := rasterImageType(data)
	if !ok {
		return nil, "", "", ErrInvalidCover
	}

	return data, contentType, extension, nil
}

func (s *Service) UploadArtistAvatar(
	ctx context.Context,
	id string,
	file io.Reader,
	size int64,
) error {
	data, contentType, extension, err := readRasterImage(file, size)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	header := &multipart.FileHeader{
		Filename: "avatar" + extension,
		Header:   map[string][]string{"Content-Type": {contentType}},
		Size:     int64(len(data)),
	}
	object, err := s.UploadFile(ctx, bytes.NewReader(data), header, "avatars")
	if err != nil {
		return err
	}

	return s.ReplaceArtistAvatar(ctx, id, object)
}

func (s *Service) ReplaceArtistAvatar(
	ctx context.Context,
	id string,
	newAvatar string,
) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		s.removeObject(ctx, newAvatar)

		return err
	}
	defer tx.Rollback(ctx)

	var oldAvatar sql.NullString
	err = tx.QueryRow(ctx, `SELECT avatar_object FROM artists WHERE id = $1 FOR UPDATE`, id).Scan(&oldAvatar)
	if err != nil {
		s.removeObject(ctx, newAvatar)

		return serviceError(err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE artists
		SET avatar_object = $1, avatar_source_url = NULL,
			avatar_attribution = NULL, avatar_license = NULL
		WHERE id = $2
	`, newAvatar, id)
	if err != nil {
		s.removeObject(ctx, newAvatar)

		return serviceError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		// A failed commit can be ambiguous; keep both objects rather than break a committed row.
		return err
	}

	if oldAvatar.Valid && oldAvatar.String != newAvatar {
		s.removeObject(ctx, oldAvatar.String)
	}

	return nil
}

func (s *Service) DeleteArtistAvatar(ctx context.Context, id string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var avatar sql.NullString
	err = tx.QueryRow(ctx, `SELECT avatar_object FROM artists WHERE id = $1 FOR UPDATE`, id).Scan(&avatar)
	if err != nil {
		return serviceError(err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE artists
		SET avatar_object = NULL, avatar_source_url = NULL,
			avatar_attribution = NULL, avatar_license = NULL
		WHERE id = $1
	`, id)
	if err != nil {
		return serviceError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		// A failed commit can be ambiguous; keep the object rather than break a retained reference.
		return err
	}

	if avatar.Valid {
		s.removeObject(ctx, avatar.String)
	}

	return nil
}

func (s *Service) GetArtistAvatarObject(ctx context.Context, id string) (string, error) {
	var object string
	err := s.DB.QueryRow(ctx, `
		SELECT avatar_object
		FROM artists
		WHERE id = $1 AND avatar_object IS NOT NULL
	`, id).Scan(&object)

	return object, serviceError(err)
}
