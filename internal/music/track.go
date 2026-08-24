package music

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/lalyod/utane/internal/model"
)

type lyricsLookupResponse struct {
	TrackName    string  `json:"trackName"`
	ArtistName   string  `json:"artistName"`
	AlbumName    string  `json:"albumName"`
	Duration     float64 `json:"duration"`
	SyncedLyrics string  `json:"syncedLyrics"`
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
