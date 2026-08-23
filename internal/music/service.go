package music

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"

	minioClient "github.com/lalyod/utane/internal/minio"
	"github.com/lalyod/utane/internal/model"
)

type Service struct {
	Minio               *minioClient.Client
	DB                  *pgxpool.Pool
	HTTPClient          *http.Client
	LyricsEndpoint      string
	MusicBrainzEndpoint string
	CoverArtEndpoint    string
	MusicBrainzInterval time.Duration

	musicBrainzMu   sync.Mutex
	musicBrainzLast time.Time
}

var (
	ErrNotFound       = errors.New("not found")
	ErrConflict       = errors.New("conflict")
	ErrInvalid        = errors.New("invalid input")
	ErrLyricsNotFound = errors.New("synchronized lyrics not found")
	ErrUpstream       = errors.New("upstream service failed")
	ErrCoverTooLarge  = errors.New("cover exceeds 15 MiB")
	ErrInvalidCover   = errors.New("cover is not a supported raster image")
)

const (
	maxCoverSize = 15 << 20
	userAgent    = "Utane/1.0 (https://github.com/lalyod/utane)"
)

type CoverCandidate struct {
	ReleaseID string `json:"release_id"`
	Title     string `json:"title"`
	Artist    string `json:"artist"`
	Year      int    `json:"year"`
	Thumbnail string `json:"thumbnail"`
}

type musicBrainzReleaseResponse struct {
	Releases []struct {
		ID           string `json:"id"`
		Title        string `json:"title"`
		Date         string `json:"date"`
		ArtistCredit []struct {
			Name       string `json:"name"`
			JoinPhrase string `json:"joinphrase"`
		} `json:"artist-credit"`
	} `json:"releases"`
}

type lyricsLookupResponse struct {
	TrackName    string  `json:"trackName"`
	ArtistName   string  `json:"artistName"`
	AlbumName    string  `json:"albumName"`
	Duration     float64 `json:"duration"`
	SyncedLyrics string  `json:"syncedLyrics"`
}

func serviceError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}

	switch pgErr.Code {
	case "23505":
		return fmt.Errorf("%w: value already exists", ErrConflict)
	case "23503", "22P02":
		return ErrInvalid
	default:
		return err
	}
}

func (s *Service) CreateArtist(
	ctx context.Context,
	name string,
) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO artists ( name ) VALUES ( $1 )`, name)

	return serviceError(err)
}

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
		VALUES ($1, $2, $3, $4)
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
	err = tx.QueryRow(ctx, `SELECT cover_object FROM albums WHERE id = $1 FOR UPDATE`, id).Scan(&oldCover)
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

	_, err = tx.Exec(ctx, `UPDATE albums SET title = $1, year = $2, cover_object = $3 WHERE id = $4`, title, albumYear, cover, id)
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
	err = tx.QueryRow(ctx, `SELECT cover_object FROM albums WHERE id = $1 FOR UPDATE`, id).Scan(&cover)
	if err != nil {
		return serviceError(err)
	}

	if _, err = tx.Exec(ctx, `UPDATE tracks SET album_id = NULL, track_number = NULL WHERE album_id = $1`, id); err != nil {
		return serviceError(err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM albums WHERE id = $1`, id); err != nil {
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
		SELECT albums.title, COALESCE(albums.year::text, ''), artists.name
		FROM albums
		JOIN artists ON artists.id = albums.artist_id
		WHERE albums.id = $1
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

func (s *Service) SearchTrackCovers(ctx context.Context, id string) ([]CoverCandidate, error) {
	var title string
	var artist string
	var hasAlbum bool
	var hasCover bool
	err := s.DB.QueryRow(ctx, `
		SELECT tracks.title, artists.name, tracks.album_id IS NOT NULL, tracks.cover_object IS NOT NULL
		FROM tracks
		JOIN artists ON artists.id = tracks.artist_id
		WHERE tracks.id = $1
	`, id).Scan(&title, &artist, &hasAlbum, &hasCover)
	if err != nil {
		return nil, serviceError(err)
	}
	if err := trackCoverEligibility(hasAlbum, hasCover); err != nil {
		return nil, err
	}

	if err := s.waitForMusicBrainz(ctx); err != nil {
		return nil, err
	}

	candidates, err := searchCovers(ctx, s.HTTPClient, s.MusicBrainzEndpoint, s.CoverArtEndpoint, title, "", artist)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUpstream, err)
	}

	return candidates, nil
}

func trackCoverEligibility(hasAlbum bool, hasCover bool) error {
	if hasAlbum && !hasCover {
		return fmt.Errorf("%w: album track inherits its album cover", ErrConflict)
	}

	return nil
}

func (s *Service) waitForMusicBrainz(ctx context.Context) error {
	if s.MusicBrainzInterval <= 0 {
		return nil
	}

	s.musicBrainzMu.Lock()
	defer s.musicBrainzMu.Unlock()

	wait := time.Until(s.musicBrainzLast.Add(s.MusicBrainzInterval))
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	s.musicBrainzLast = time.Now()

	return nil
}

func searchCovers(
	ctx context.Context,
	client *http.Client,
	musicBrainzEndpoint string,
	coverArtEndpoint string,
	title string,
	year string,
	artist string,
) ([]CoverCandidate, error) {
	terms := []string{musicBrainzTerm("release", title), musicBrainzTerm("artist", artist)}
	if year != "" {
		terms = append(terms, musicBrainzTerm("date", year))
	}

	requestURL, err := url.Parse(musicBrainzEndpoint)
	if err != nil {
		return nil, err
	}
	query := requestURL.Query()
	query.Set("query", strings.Join(terms, " AND "))
	query.Set("fmt", "json")
	query.Set("limit", "8")
	requestURL.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", userAgent)

	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MusicBrainz search failed: %s", response.Status)
	}

	var result musicBrainzReleaseResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result); err != nil {
		return nil, err
	}

	candidates := make([]CoverCandidate, 0, len(result.Releases))
	for _, release := range result.Releases {
		hasCover, err := coverArtAvailable(ctx, client, coverArtEndpoint, release.ID)
		if err != nil {
			return nil, err
		}
		if !hasCover {
			continue
		}

		thumbnail, err := coverArtURL(coverArtEndpoint, release.ID, "front-250")
		if err != nil {
			return nil, err
		}
		var artistName strings.Builder
		for _, credit := range release.ArtistCredit {
			artistName.WriteString(credit.Name)
			artistName.WriteString(credit.JoinPhrase)
		}
		releaseYear, _ := strconv.Atoi(strings.SplitN(release.Date, "-", 2)[0])
		candidates = append(candidates, CoverCandidate{
			ReleaseID: release.ID,
			Title:     release.Title,
			Artist:    artistName.String(),
			Year:      releaseYear,
			Thumbnail: thumbnail,
		})
	}

	return candidates, nil
}

func coverArtAvailable(ctx context.Context, client *http.Client, endpoint string, releaseID string) (bool, error) {
	requestURL, err := coverArtURL(endpoint, releaseID, "front-250")
	if err != nil {
		return false, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, requestURL, nil)
	if err != nil {
		return false, err
	}
	request.Header.Set("User-Agent", userAgent)

	probeClient := *client
	probeClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := probeClient.Do(request)
	if err != nil {
		return false, err
	}
	response.Body.Close()

	switch {
	case response.StatusCode == http.StatusNotFound:
		return false, nil
	case response.StatusCode >= 200 && response.StatusCode < 400:
		return true, nil
	default:
		return false, fmt.Errorf("cover availability failed: %s", response.Status)
	}
}

func musicBrainzTerm(field string, value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)

	return field + `:"` + value + `"`
}

func coverArtURL(endpoint string, releaseID string, image string) (string, error) {
	requestURL, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	requestURL.Path = path.Join(requestURL.Path, "release", releaseID, image)
	requestURL.RawPath = ""
	requestURL.RawQuery = ""
	requestURL.Fragment = ""

	return requestURL.String(), nil
}

func (s *Service) FetchAlbumCover(ctx context.Context, albumID string, releaseID string) error {
	var albumExists bool
	if err := s.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM albums WHERE id = $1)`, albumID).Scan(&albumExists); err != nil {
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

func (s *Service) FetchTrackCover(ctx context.Context, trackID string, releaseID string) error {
	var hasAlbum bool
	var hasCover bool
	err := s.DB.QueryRow(ctx, `
		SELECT album_id IS NOT NULL, cover_object IS NOT NULL
		FROM tracks
		WHERE id = $1
	`, trackID).Scan(&hasAlbum, &hasCover)
	if err != nil {
		return serviceError(err)
	}
	if err := trackCoverEligibility(hasAlbum, hasCover); err != nil {
		return err
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

	return s.ReplaceTrackCover(ctx, trackID, object)
}

func downloadCover(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	releaseID string,
) ([]byte, string, string, error) {
	requestURL, err := coverArtURL(endpoint, releaseID, "front")
	if err != nil {
		return nil, "", "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, "", "", err
	}
	request.Header.Set("User-Agent", userAgent)

	coverClient, err := restrictedCoverClient(client, endpoint)
	if err != nil {
		return nil, "", "", err
	}
	response, err := coverClient.Do(request)
	if err != nil {
		return nil, "", "", err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, "", "", fmt.Errorf("cover download failed: %s", response.Status)
	}
	return readRasterImage(response.Body, response.ContentLength)
}

func rasterImageType(data []byte) (string, string, bool) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 25_000_000 {
		return "", "", false
	}
	if _, decodedFormat, err := image.Decode(bytes.NewReader(data)); err != nil || decodedFormat != format {
		return "", "", false
	}

	switch format {
	case "jpeg":
		return "image/jpeg", ".jpg", true
	case "png":
		return "image/png", ".png", true
	case "gif":
		return "image/gif", ".gif", true
	default:
		return "", "", false
	}
}

func restrictedCoverClient(client *http.Client, endpoint string) (*http.Client, error) {
	allowedOrigin, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}

	coverClient := *client
	coverClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many cover redirects")
		}
		if request.URL.Scheme == allowedOrigin.Scheme && request.URL.Host == allowedOrigin.Host {
			return nil
		}
		host := strings.ToLower(request.URL.Hostname())
		if request.URL.Scheme == "https" && (host == "archive.org" || strings.HasSuffix(host, ".archive.org")) {
			return nil
		}

		return fmt.Errorf("cover redirect to untrusted host %q", request.URL.Host)
	}

	return &coverClient, nil
}

func (s *Service) ReplaceAlbumCover(ctx context.Context, id string, newCover string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		s.removeObject(ctx, newCover)

		return err
	}
	defer tx.Rollback(ctx)

	var oldCover sql.NullString
	err = tx.QueryRow(ctx, `SELECT cover_object FROM albums WHERE id = $1 FOR UPDATE`, id).Scan(&oldCover)
	if err != nil {
		s.removeObject(ctx, newCover)

		return serviceError(err)
	}

	if _, err = tx.Exec(ctx, `UPDATE albums SET cover_object = $1 WHERE id = $2`, newCover, id); err != nil {
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

func (s *Service) ReplaceTrackCover(ctx context.Context, id string, newCover string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		s.removeObject(ctx, newCover)

		return err
	}
	defer tx.Rollback(ctx)

	var hasAlbum bool
	var oldCover sql.NullString
	err = tx.QueryRow(ctx, `
		SELECT album_id IS NOT NULL, cover_object
		FROM tracks
		WHERE id = $1
		FOR UPDATE
	`, id).Scan(&hasAlbum, &oldCover)
	if err != nil {
		s.removeObject(ctx, newCover)

		return serviceError(err)
	}
	if err := trackCoverEligibility(hasAlbum, oldCover.Valid); err != nil {
		s.removeObject(ctx, newCover)

		return err
	}

	if _, err = tx.Exec(ctx, `UPDATE tracks SET cover_object = $1 WHERE id = $2`, newCover, id); err != nil {
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

func (s *Service) DeleteTrackCover(ctx context.Context, id string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var cover sql.NullString
	err = tx.QueryRow(ctx, `SELECT cover_object FROM tracks WHERE id = $1 FOR UPDATE`, id).Scan(&cover)
	if err != nil {
		return serviceError(err)
	}

	if _, err = tx.Exec(ctx, `UPDATE tracks SET cover_object = NULL WHERE id = $1`, id); err != nil {
		return serviceError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		// A failed commit can be ambiguous; keep the object rather than break a retained reference.
		return err
	}

	if cover.Valid {
		s.removeObject(ctx, cover.String)
	}

	return nil
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
		WHERE id = $1
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
		SET name = $1
		WHERE id = $2
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
	err = tx.QueryRow(ctx, `SELECT avatar_object FROM artists WHERE id = $1 FOR UPDATE`, id).Scan(&avatar)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return serviceError(err)
	}

	var hasMusic bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM albums WHERE artist_id = $1)
			OR EXISTS (SELECT 1 FROM tracks WHERE artist_id = $1)
	`, id).Scan(&hasMusic)
	if err != nil {
		return serviceError(err)
	}
	if hasMusic {
		return fmt.Errorf("%w: artist has albums or tracks", ErrConflict)
	}

	if _, err = tx.Exec(ctx, `DELETE FROM artists WHERE id = $1`, id); err != nil {
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

func (s *Service) AlbumBelongsToArtist(ctx context.Context, artistID string, albumID string) (bool, error) {
	if albumID == "" {
		return true, nil
	}

	var ok bool
	err := s.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM albums
			WHERE id = $1 AND artist_id = $2
		)
	`, albumID, artistID).Scan(&ok)

	return ok, err
}

func (s *Service) CreateTrack(
	ctx context.Context,
	artistID string,
	albumID string,
	title string,
	duration int,
	trackNumber *int,
	object string,
) (string, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		s.removeObject(ctx, object)

		return "", err
	}
	defer tx.Rollback(ctx)

	var album any
	if albumID != "" {
		album = albumID
	}

	var number any
	if trackNumber != nil {
		number = *trackNumber
	}

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO tracks (
			artist_id,
			album_id,
			title,
			duration,
			object_path,
			track_number
		) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, artistID, album, title, duration, object, number).Scan(&id)
	if err != nil {
		s.removeObject(ctx, object)

		return "", serviceError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		// A failed commit can be ambiguous; keep the object rather than break a committed row.
		return "", err
	}

	return id, nil
}

func (s *Service) PatchTrack(
	ctx context.Context,
	id string,
	artistID string,
	albumID string,
	title string,
	trackNumber *int,
	duration *int,
	newObject string,
) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		if newObject != "" {
			s.removeObject(ctx, newObject)
		}

		return err
	}
	defer tx.Rollback(ctx)

	var oldObject string
	var oldDuration int
	err = tx.QueryRow(ctx, `SELECT object_path, duration FROM tracks WHERE id = $1 FOR UPDATE`, id).Scan(&oldObject, &oldDuration)
	if err != nil {
		if newObject != "" {
			s.removeObject(ctx, newObject)
		}

		return serviceError(err)
	}

	if albumID != "" {
		var albumOK bool
		err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM albums WHERE id = $1 AND artist_id = $2)`, albumID, artistID).Scan(&albumOK)
		if err != nil || !albumOK {
			if newObject != "" {
				s.removeObject(ctx, newObject)
			}
			if err != nil {
				return serviceError(err)
			}

			return fmt.Errorf("%w: album does not belong to artist", ErrInvalid)
		}
	}

	var album any
	if albumID != "" {
		album = albumID
	}
	var number any
	if trackNumber != nil {
		number = *trackNumber
	}
	if duration == nil {
		duration = &oldDuration
	}
	object := oldObject
	if newObject != "" {
		object = newObject
	}

	_, err = tx.Exec(ctx, `
		UPDATE tracks
		SET artist_id = $1, album_id = $2, title = $3, track_number = $4, duration = $5, object_path = $6
		WHERE id = $7
	`, artistID, album, title, number, *duration, object, id)
	if err != nil {
		if newObject != "" {
			s.removeObject(ctx, newObject)
		}

		return serviceError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		// A failed commit can be ambiguous; keep both objects rather than break a committed row.
		return err
	}

	if newObject != "" && oldObject != newObject {
		s.removeObject(ctx, oldObject)
	}

	return nil
}

func (s *Service) DeleteTrack(ctx context.Context, id string) error {
	var object string
	var cover sql.NullString
	err := s.DB.QueryRow(ctx, `DELETE FROM tracks WHERE id = $1 RETURNING object_path, cover_object`, id).Scan(&object, &cover)
	if err != nil {
		return serviceError(err)
	}

	s.removeObject(ctx, object)
	if cover.Valid {
		s.removeObject(ctx, cover.String)
	}

	return nil
}

func (s *Service) GetTracks(ctx context.Context) ([]model.Track, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT tracks.id, tracks.artist_id, artists.name, COALESCE(tracks.album_id::text, ''),
			COALESCE(albums.title, ''), tracks.title, tracks.duration,
			COALESCE(tracks.track_number, 0), tracks.cover_object IS NOT NULL,
			tracks.synced_lyrics, tracks.created_at
		FROM tracks
		JOIN artists ON artists.id = tracks.artist_id
		LEFT JOIN albums ON albums.id = tracks.album_id
		ORDER BY artists.name ASC, albums.year ASC NULLS LAST, albums.title ASC NULLS LAST,
			tracks.track_number ASC NULLS LAST, tracks.title ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tracks := []model.Track{}

	for rows.Next() {
		var track model.Track
		if err := rows.Scan(
			&track.ID,
			&track.ArtistID,
			&track.ArtistName,
			&track.AlbumID,
			&track.AlbumTitle,
			&track.Title,
			&track.Duration,
			&track.TrackNumber,
			&track.HasCover,
			&track.SyncedLyrics,
			&track.CreatedAt,
		); err != nil {
			return nil, err
		}
		tracks = append(tracks, track)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return tracks, nil
}

func (s *Service) SetTrackLyrics(ctx context.Context, id string, lyrics string) error {
	result, err := s.DB.Exec(ctx, `
		UPDATE tracks
		SET synced_lyrics = $1
		WHERE id = $2
	`, lyrics, id)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return ErrLyricsNotFound
	}

	return nil
}

func (s *Service) LookupTrackLyrics(ctx context.Context, id string) (string, error) {
	var artistName string
	var albumTitle string
	var trackTitle string
	var duration int
	err := s.DB.QueryRow(ctx, `
		SELECT artists.name, COALESCE(albums.title, ''), tracks.title, tracks.duration
		FROM tracks
		JOIN artists ON artists.id = tracks.artist_id
		LEFT JOIN albums ON albums.id = tracks.album_id
		WHERE tracks.id = $1
	`, id).Scan(&artistName, &albumTitle, &trackTitle, &duration)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrLyricsNotFound
	}
	if err != nil {
		return "", err
	}

	return lookupLyrics(ctx, s.HTTPClient, s.LyricsEndpoint, artistName, albumTitle, trackTitle, duration)
}

func lookupLyrics(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	artistName string,
	albumTitle string,
	trackTitle string,
	duration int,
) (string, error) {
	exactParams := url.Values{
		"artist_name": {artistName},
		"track_name":  {trackTitle},
		"album_name":  {albumTitle},
		"duration":    {fmt.Sprint(duration)},
	}
	exactURL, err := lyricsRequestURL(endpoint, "", exactParams)
	if err != nil {
		return "", err
	}

	var exact lyricsLookupResponse
	err = getLyrics(ctx, client, exactURL, &exact)
	if err != nil && !errors.Is(err, ErrLyricsNotFound) {
		return "", err
	}
	if exact.SyncedLyrics != "" && !latinOnly(exact.SyncedLyrics) {
		return exact.SyncedLyrics, nil
	}

	searchParams := url.Values{
		"artist_name": {artistName},
		"track_name":  {trackTitle},
		"album_name":  {albumTitle},
	}
	searchURL, err := lyricsRequestURL(endpoint, "search", searchParams)
	if err != nil {
		return "", err
	}

	var results []lyricsLookupResponse
	if err := getLyrics(ctx, client, searchURL, &results); err != nil {
		if exact.SyncedLyrics != "" {
			return exact.SyncedLyrics, nil
		}

		return "", err
	}

	bestRank := 0
	lyrics := exact.SyncedLyrics
	if lyrics != "" {
		bestRank = 1
	}
	for _, result := range results {
		if result.SyncedLyrics == "" ||
			!sameMetadata(result, artistName, albumTitle, trackTitle, duration) {
			continue
		}

		rank := 1
		if hasNativeScript(result.SyncedLyrics) {
			rank = 2
		}
		if rank > bestRank {
			bestRank = rank
			lyrics = result.SyncedLyrics
		}
	}
	if lyrics == "" {
		return "", ErrLyricsNotFound
	}

	return lyrics, nil
}

func lyricsRequestURL(endpoint string, resource string, params url.Values) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	if resource != "" {
		dir := path.Dir(parsed.Path)
		if dir == "." || dir == "/" {
			parsed.Path = "/" + resource
		} else {
			parsed.Path = path.Join(dir, resource)
		}
		parsed.RawPath = ""
	}

	query := parsed.Query()
	for key, values := range params {
		query.Del(key)
		for _, value := range values {
			query.Add(key, value)
		}
	}
	parsed.RawQuery = query.Encode()

	return parsed.String(), nil
}

func getLyrics(ctx context.Context, client *http.Client, requestURL string, result any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "Utane/1.0")

	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusNotFound {
		return ErrLyricsNotFound
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("lyrics lookup failed: %s", response.Status)
	}

	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(result)
}

func sameMetadata(result lyricsLookupResponse, artistName string, albumTitle string, trackTitle string, duration int) bool {
	return normalizedMetadata(result.ArtistName) == normalizedMetadata(artistName) &&
		normalizedMetadata(result.TrackName) == normalizedMetadata(trackTitle) &&
		(albumTitle == "" || normalizedMetadata(result.AlbumName) == normalizedMetadata(albumTitle)) &&
		math.Abs(result.Duration-float64(duration)) <= 3
}

func normalizedMetadata(value string) string {
	var normalized strings.Builder
	for _, char := range value {
		if unicode.IsLetter(char) || unicode.IsNumber(char) {
			normalized.WriteRune(unicode.ToLower(char))
		}
	}

	return normalized.String()
}

func latinOnly(value string) bool {
	hasLetters := false
	for _, char := range lyricBody(value) {
		if !unicode.IsLetter(char) {
			continue
		}
		hasLetters = true
		if !unicode.In(char, unicode.Latin) {
			return false
		}
	}

	return hasLetters
}

func hasNativeScript(value string) bool {
	for _, char := range lyricBody(value) {
		if unicode.In(char, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
			return true
		}
	}

	return false
}

func lyricBody(value string) string {
	var body strings.Builder
	foundTimedLine := false
	for _, line := range strings.Split(value, "\n") {
		close := strings.IndexByte(line, ']')
		if close <= 1 || line[0] != '[' || !validLRCTimestamp(line[1:close]) {
			continue
		}
		foundTimedLine = true
		body.WriteString(line[close+1:])
		body.WriteByte('\n')
	}
	if foundTimedLine {
		return body.String()
	}

	return value
}

func validLRCTimestamp(value string) bool {
	minutes, seconds, ok := strings.Cut(value, ":")
	if !ok {
		return false
	}
	if _, err := strconv.ParseUint(minutes, 10, 64); err != nil {
		return false
	}
	secondsValue, err := strconv.ParseFloat(seconds, 64)

	return err == nil && secondsValue >= 0 && secondsValue < 60
}

func (s *Service) GetTrackObject(ctx context.Context, id string) (string, error) {
	var object string
	err := s.DB.QueryRow(ctx, `
		SELECT object_path
		FROM tracks
		WHERE id = $1
	`, id).Scan(&object)

	return object, err
}

func (s *Service) GetTrackCoverObject(ctx context.Context, id string) (string, error) {
	var object string
	err := s.DB.QueryRow(ctx, `
		SELECT cover_object
		FROM tracks
		WHERE id = $1 AND cover_object IS NOT NULL
	`, id).Scan(&object)

	return object, serviceError(err)
}

func (s *Service) GetAlbumCoverObject(ctx context.Context, id string) (string, error) {
	var object string
	err := s.DB.QueryRow(ctx, `
		SELECT cover_object
		FROM albums
		WHERE id = $1 AND cover_object IS NOT NULL
	`, id).Scan(&object)

	return object, err
}

func (s *Service) UploadTrack(
	ctx context.Context,
	file multipart.File,
	header *multipart.FileHeader,
) (string, error) {
	return s.UploadFile(ctx, file, header, "tracks")
}

func (s *Service) UploadCover(
	ctx context.Context,
	file multipart.File,
	header *multipart.FileHeader,
) (string, error) {
	return s.UploadFile(ctx, file, header, "covers")
}

func (s *Service) UploadFile(
	ctx context.Context,
	file io.Reader,
	header *multipart.FileHeader,
	dir string,
) (string, error) {
	objectName := dir + "/" + uuid.New().String() + "-" + filepath.Base(header.Filename)

	_, err := s.Minio.PutObject(
		ctx,
		s.Minio.Bucket,
		objectName,
		file,
		header.Size,
		minio.PutObjectOptions{
			ContentType: header.Header.Get("Content-Type"),
		},
	)
	if err != nil {
		return "", err
	}

	return objectName, nil
}

func (s *Service) removeObject(ctx context.Context, object string) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	if err := s.Minio.RemoveObject(cleanupCtx, s.Minio.Bucket, object, minio.RemoveObjectOptions{}); err != nil {
		slog.Error("failed to remove media object", "object", object, "error", err)
	}
}

func (s *Service) OpenObject(
	ctx context.Context,
	object string,
) (*minio.Object, minio.ObjectInfo, error) {
	file, err := s.Minio.GetObject(ctx, s.Minio.Bucket, object, minio.GetObjectOptions{})
	if err != nil {
		return nil, minio.ObjectInfo{}, err
	}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		if minio.ToErrorResponse(err).StatusCode == http.StatusNotFound {
			return nil, minio.ObjectInfo{}, ErrNotFound
		}
		return nil, minio.ObjectInfo{}, err
	}

	return file, info, nil
}
