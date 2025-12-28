package music

import (
	"context"
	"mime/multipart"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	minioClient "github.com/lalyod/mysong/internal/minio"
	"github.com/lalyod/mysong/internal/model"
	"github.com/minio/minio-go/v7"
)

type Service struct {
	Minio *minioClient.Client
	DB    *pgxpool.Pool
}

func (s *Service) CreateArtist(
	ctx context.Context,
	name string,
) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO artists ( name ) VALUES ( $1 )`, name)

	return err
}

func (s *Service) GetArtists(ctx context.Context) ([]model.Artist, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT id, name, created_at
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
		if err := rows.Scan(&artist.ID, &artist.Name, &artist.CreatedAt); err != nil {
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
	rows, err := s.DB.Query(ctx, `
		SELECT id, name, created_at
		FROM artists
		WHERE id = $1
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	artist := model.Artist{}

	for rows.Next() {
		if err := rows.Scan(&artist.ID, &artist.Name, &artist.CreatedAt); err != nil {
			return nil, err
		}
	}

	return &artist, nil
}

func (s *Service) PatchArtist(ctx context.Context, id string, name string) error {
	_, err := s.DB.Exec(ctx, `
		UPDATE artists 
		SET name = $1
		WHERE id = $2
		`, name, id)

	return err
}

func (s *Service) CreateTrack(
	ctx context.Context,
	artistID string,
	albumID string,
	title string,
	duration string,
	object string,
) error {
	_, err := s.DB.Exec(ctx, `
		INSERT INTO tracks (
			artist_id,
			album_id,
			title,
			duration,
			object_path,
		) VALUES ($1, $2, $3, $4, $5)
	`, artistID, albumID, title, duration, object)

	return err
}

func (s *Service) UploadTrack(
	ctx context.Context,
	file multipart.File,
	header *multipart.FileHeader,
) (string, error) {
	objectName := "tracks/" + uuid.New().String() + "-" + header.Filename

	_, err := s.Minio.PutObject(
		ctx,
		s.Minio.Bucket,
		objectName,
		file,
		header.Size,
		minio.PutObjectOptions{
			ContentType: header.Header.Get("content-type"),
		},
	)

	if err != nil {
		return "", err
	}

	return objectName, nil
}

func (s *Service) GetStreamURL(
	ctx context.Context,
	object string,
) (string, error) {
	url, err := s.Minio.PresignedGetObject(
		ctx,
		s.Minio.Bucket,
		object,
		60*10, // 10 minutes
		nil,
	)

	if err != nil {
		return "", err
	}

	return url.String(), nil
}
