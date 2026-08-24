package music

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"

	minioClient "github.com/lalyod/utane/internal/minio"
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
