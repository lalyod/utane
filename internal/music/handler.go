package music

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
	"github.com/google/uuid"
)

type Handler struct {
	Service *Service
}

type Response struct {
	Message string `json:"message"`
}

type TrackResponse struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

type ArtistPayload struct {
	Name string `json:"name"`
}

type LyricsPayload struct {
	Lyrics string `json:"lyrics"`
}

type CoverFetchPayload struct {
	ReleaseID string `json:"release_id"`
}

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/tracks", h.GetTracks)
	r.Get("/tracks/{id}/audio", h.StreamTrack)
	r.Get("/tracks/{id}/cover", h.GetTrackCover)
	r.Get("/tracks/{id}/cover/search", h.SearchTrackCovers)
	r.Get("/tracks/{id}/lyrics/lookup", h.LookupTrackLyrics)

	r.Get("/albums", h.GetAlbums)
	r.Get("/albums/{id}/cover", h.GetAlbumCover)
	r.Get("/albums/{id}/cover/search", h.SearchAlbumCovers)

	r.Get("/artists", h.GetArtists)
	r.Get("/artists/{id}", h.GetArtist)
	r.Get("/artists/{id}/avatar", h.GetArtistAvatar)

	r.Post("/tracks", h.CreateTrack)
	r.Patch("/tracks/{id}", h.PatchTrack)
	r.Delete("/tracks/{id}", h.DeleteTrack)
	r.Post("/tracks/{id}/cover/fetch", h.FetchTrackCover)
	r.Delete("/tracks/{id}/cover", h.DeleteTrackCover)
	r.Patch("/tracks/{id}/lyrics", h.PatchTrackLyrics)
	r.Post("/albums", h.CreateAlbum)
	r.Patch("/albums/{id}", h.PatchAlbum)
	r.Delete("/albums/{id}", h.DeleteAlbum)
	r.Post("/albums/{id}/cover/fetch", h.FetchAlbumCover)
	r.Post("/artists", h.CreateArtist)
	r.Patch("/artists/{id}", h.PatchArtist)
	r.Delete("/artists/{id}", h.DeleteArtist)
	r.Put("/artists/{id}/avatar", h.PutArtistAvatar)
	r.Delete("/artists/{id}/avatar", h.DeleteArtistAvatar)

	return r
}

func writeServiceError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, ErrUpstream):
		status = http.StatusBadGateway
	}

	http.Error(w, err.Error(), status)
}

func validID(id string) bool {
	_, err := uuid.Parse(id)

	return err == nil
}

func validUploadType(value string, kind string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}

	if kind == "audio" {
		return strings.HasPrefix(mediaType, "audio/")
	}

	switch mediaType {
	case "image/avif", "image/gif", "image/jpeg", "image/png", "image/webp":
		return true
	default:
		return false
	}
}

func validUpload(file multipart.File, contentType string, kind string) bool {
	if !validUploadType(contentType, kind) {
		return false
	}

	buffer := make([]byte, 512)
	n, err := file.Read(buffer)
	if err != nil && err != io.EOF {
		return false
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false
	}

	detected, _, _ := mime.ParseMediaType(http.DetectContentType(buffer[:n]))
	if kind == "image" {
		return validUploadType(detected, "image")
	}
	declared, _, _ := mime.ParseMediaType(contentType)

	return strings.HasPrefix(detected, "audio/") ||
		detected == "application/ogg" ||
		detected == "video/mp4" ||
		declared == "audio/flac" && bytes.HasPrefix(buffer[:n], []byte("fLaC"))
}

func (h *Handler) CreateTrack(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 512<<20)

	audio, header, err := r.FormFile("audio")
	if err != nil {
		http.Error(w, "audio is required", http.StatusBadRequest)
		return
	}
	defer audio.Close()
	if !validUpload(audio, header.Header.Get("Content-Type"), "audio") {
		http.Error(w, "audio must be an audio file", http.StatusBadRequest)
		return
	}

	artistID := strings.TrimSpace(r.FormValue("artist_id"))
	albumID := strings.TrimSpace(r.FormValue("album_id"))
	title := strings.TrimSpace(r.FormValue("title"))
	durationText := strings.TrimSpace(r.FormValue("duration"))
	trackNumberText := strings.TrimSpace(r.FormValue("track_number"))

	if artistID == "" || title == "" || durationText == "" {
		http.Error(w, "artist_id, title, duration, and audio are required", http.StatusBadRequest)
		return
	}
	if !validID(artistID) || albumID != "" && !validID(albumID) {
		http.Error(w, "artist_id and album_id must be valid UUIDs", http.StatusBadRequest)
		return
	}

	duration, err := strconv.Atoi(durationText)
	if err != nil || duration <= 0 {
		http.Error(w, "duration must be a positive number of seconds", http.StatusBadRequest)
		return
	}

	var trackNumber *int
	if trackNumberText != "" {
		number, err := strconv.Atoi(trackNumberText)
		if err != nil || number <= 0 {
			http.Error(w, "track_number must be a positive number", http.StatusBadRequest)
			return
		}
		trackNumber = &number
	}

	albumOK, err := h.Service.AlbumBelongsToArtist(r.Context(), artistID, albumID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if !albumOK {
		http.Error(w, "album does not belong to artist", http.StatusBadRequest)
		return
	}

	object, err := h.Service.UploadTrack(r.Context(), audio, header)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	id, err := h.Service.CreateTrack(r.Context(), artistID, albumID, title, duration, trackNumber, object)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	response := &TrackResponse{
		ID:      id,
		Message: "Create track successfully",
	}

	render.JSON(w, r, response)
}

func (h *Handler) PatchTrack(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 512<<20)

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	id := chi.URLParam(r, "id")
	artistID := strings.TrimSpace(r.FormValue("artist_id"))
	albumID := strings.TrimSpace(r.FormValue("album_id"))
	title := strings.TrimSpace(r.FormValue("title"))
	if !validID(id) || !validID(artistID) || albumID != "" && !validID(albumID) {
		http.Error(w, "id, artist_id, and album_id must be valid UUIDs", http.StatusBadRequest)
		return
	}
	if title == "" {
		http.Error(w, "artist_id and title are required", http.StatusBadRequest)
		return
	}

	trackNumber, ok := optionalPositiveInt(w, r.FormValue("track_number"), "track_number")
	if !ok {
		return
	}
	duration, ok := optionalPositiveInt(w, r.FormValue("duration"), "duration")
	if !ok {
		return
	}

	audioObject := ""
	audio, header, err := r.FormFile("audio")
	if err == nil {
		defer audio.Close()
		if !validUpload(audio, header.Header.Get("Content-Type"), "audio") {
			http.Error(w, "audio must be an audio file", http.StatusBadRequest)
			return
		}

		audioObject, err = h.Service.UploadTrack(r.Context(), audio, header)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else if err != http.ErrMissingFile {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	err = h.Service.PatchTrack(r.Context(), id, artistID, albumID, title, trackNumber, duration, audioObject)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	render.JSON(w, r, Response{Message: "Track updated"})
}

func optionalPositiveInt(w http.ResponseWriter, text string, field string) (*int, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, true
	}

	value, err := strconv.Atoi(text)
	if err != nil || value <= 0 {
		http.Error(w, field+" must be a positive number", http.StatusBadRequest)
		return nil, false
	}

	return &value, true
}

func (h *Handler) DeleteTrack(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validID(id) {
		http.Error(w, "id must be a valid UUID", http.StatusBadRequest)
		return
	}

	if err := h.Service.DeleteTrack(r.Context(), id); err != nil {
		writeServiceError(w, err)
		return
	}

	render.JSON(w, r, Response{Message: "Track deleted"})
}

func (h *Handler) PatchTrackLyrics(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validID(id) {
		http.Error(w, "id must be a valid UUID", http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 512<<10)
	defer r.Body.Close()

	var payload LyricsPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "lyrics must be valid JSON", http.StatusBadRequest)
		return
	}

	err := h.Service.SetTrackLyrics(r.Context(), id, payload.Lyrics)
	if errors.Is(err, ErrLyricsNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	render.JSON(w, r, Response{Message: "Lyrics saved"})
}

func (h *Handler) LookupTrackLyrics(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validID(id) {
		http.Error(w, "id must be a valid UUID", http.StatusBadRequest)
		return
	}

	lyrics, err := h.Service.LookupTrackLyrics(r.Context(), id)
	if errors.Is(err, ErrLyricsNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	render.JSON(w, r, LyricsPayload{Lyrics: lyrics})
}

func (h *Handler) CreateAlbum(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)

	if err := r.ParseMultipartForm(16 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	artistID := strings.TrimSpace(r.FormValue("artist_id"))
	title := strings.TrimSpace(r.FormValue("title"))
	yearText := strings.TrimSpace(r.FormValue("year"))

	if artistID == "" || title == "" {
		http.Error(w, "artist_id and title are required", http.StatusBadRequest)
		return
	}
	if !validID(artistID) {
		http.Error(w, "artist_id must be a valid UUID", http.StatusBadRequest)
		return
	}

	var year *int
	if yearText != "" {
		value, err := strconv.Atoi(yearText)
		if err != nil || value <= 0 {
			http.Error(w, "year must be a positive number", http.StatusBadRequest)
			return
		}
		year = &value
	}

	coverObject := ""
	cover, header, err := r.FormFile("cover")
	if err == nil {
		defer cover.Close()
		if header.Size > maxCoverSize {
			http.Error(w, ErrCoverTooLarge.Error(), http.StatusBadRequest)
			return
		}
		if !validUpload(cover, header.Header.Get("Content-Type"), "image") {
			http.Error(w, "cover must be a JPEG, PNG, WebP, GIF, or AVIF image", http.StatusBadRequest)
			return
		}

		coverObject, err = h.Service.UploadCover(r.Context(), cover, header)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else if err != http.ErrMissingFile {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	err = h.Service.CreateAlbum(r.Context(), artistID, title, year, coverObject)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	render.Status(r, http.StatusCreated)
	render.JSON(w, r, Response{Message: "Album created"})
}

func (h *Handler) PatchAlbum(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)

	if err := r.ParseMultipartForm(16 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	id := chi.URLParam(r, "id")
	title := strings.TrimSpace(r.FormValue("title"))
	if !validID(id) {
		http.Error(w, "id must be a valid UUID", http.StatusBadRequest)
		return
	}
	if title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}

	year, ok := optionalPositiveInt(w, r.FormValue("year"), "year")
	if !ok {
		return
	}
	removeCover, err := strconv.ParseBool(strings.TrimSpace(r.FormValue("remove_cover")))
	if err != nil && strings.TrimSpace(r.FormValue("remove_cover")) != "" {
		http.Error(w, "remove_cover must be a boolean", http.StatusBadRequest)
		return
	}

	coverObject := ""
	cover, header, err := r.FormFile("cover")
	if err == nil {
		defer cover.Close()
		if removeCover {
			http.Error(w, "cover and remove_cover cannot be used together", http.StatusBadRequest)
			return
		}
		if header.Size > maxCoverSize {
			http.Error(w, ErrCoverTooLarge.Error(), http.StatusBadRequest)
			return
		}
		if !validUpload(cover, header.Header.Get("Content-Type"), "image") {
			http.Error(w, "cover must be a JPEG, PNG, WebP, GIF, or AVIF image", http.StatusBadRequest)
			return
		}

		coverObject, err = h.Service.UploadCover(r.Context(), cover, header)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else if err != http.ErrMissingFile {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	err = h.Service.PatchAlbum(r.Context(), id, title, year, coverObject, removeCover)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	render.JSON(w, r, Response{Message: "Album updated"})
}

func (h *Handler) DeleteAlbum(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validID(id) {
		http.Error(w, "id must be a valid UUID", http.StatusBadRequest)
		return
	}

	if err := h.Service.DeleteAlbum(r.Context(), id); err != nil {
		writeServiceError(w, err)
		return
	}

	render.JSON(w, r, Response{Message: "Album deleted"})
}

func (h *Handler) SearchAlbumCovers(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validID(id) {
		http.Error(w, "id must be a valid UUID", http.StatusBadRequest)
		return
	}

	candidates, err := h.Service.SearchAlbumCovers(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	render.JSON(w, r, candidates)
}

func (h *Handler) FetchAlbumCover(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validID(id) {
		http.Error(w, "id must be a valid UUID", http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	defer r.Body.Close()

	var payload CoverFetchPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "release_id must be valid JSON", http.StatusBadRequest)
		return
	}
	payload.ReleaseID = strings.TrimSpace(payload.ReleaseID)
	if !validID(payload.ReleaseID) {
		http.Error(w, "release_id must be a valid UUID", http.StatusBadRequest)
		return
	}

	if err := h.Service.FetchAlbumCover(r.Context(), id, payload.ReleaseID); err != nil {
		writeServiceError(w, err)
		return
	}

	render.JSON(w, r, Response{Message: "Album cover imported"})
}

func (h *Handler) SearchTrackCovers(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validID(id) {
		http.Error(w, "id must be a valid UUID", http.StatusBadRequest)
		return
	}

	candidates, err := h.Service.SearchTrackCovers(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	render.JSON(w, r, candidates)
}

func (h *Handler) FetchTrackCover(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validID(id) {
		http.Error(w, "id must be a valid UUID", http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	defer r.Body.Close()

	var payload CoverFetchPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "release_id must be valid JSON", http.StatusBadRequest)
		return
	}
	payload.ReleaseID = strings.TrimSpace(payload.ReleaseID)
	if !validID(payload.ReleaseID) {
		http.Error(w, "release_id must be a valid UUID", http.StatusBadRequest)
		return
	}

	if err := h.Service.FetchTrackCover(r.Context(), id, payload.ReleaseID); err != nil {
		writeServiceError(w, err)
		return
	}

	render.JSON(w, r, Response{Message: "Track cover imported"})
}

func (h *Handler) DeleteTrackCover(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validID(id) {
		http.Error(w, "id must be a valid UUID", http.StatusBadRequest)
		return
	}

	if err := h.Service.DeleteTrackCover(r.Context(), id); err != nil {
		writeServiceError(w, err)
		return
	}

	render.JSON(w, r, Response{Message: "Track cover removed"})
}

func (h *Handler) CreateArtist(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var payload ArtistPayload

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(payload.Name) == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	err := h.Service.CreateArtist(r.Context(), payload.Name)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	render.Status(r, http.StatusCreated)
	render.JSON(w, r, Response{
		Message: "Artist created",
	})
}

func (h *Handler) GetArtists(w http.ResponseWriter, r *http.Request) {
	artists, err := h.Service.GetArtists(r.Context())
	if err != nil {
		render.Status(r, http.StatusInternalServerError)
		render.JSON(w, r, map[string]string{
			"error": "Failed to fetch artists",
		})
		return
	}

	render.JSON(w, r, artists)
}

func (h *Handler) GetAlbums(w http.ResponseWriter, r *http.Request) {
	albums, err := h.Service.GetAlbums(r.Context())
	if err != nil {
		render.Status(r, http.StatusInternalServerError)
		render.JSON(w, r, map[string]string{
			"error": "Failed to fetch albums",
		})
		return
	}

	render.JSON(w, r, albums)
}

func (h *Handler) GetTracks(w http.ResponseWriter, r *http.Request) {
	tracks, err := h.Service.GetTracks(r.Context())
	if err != nil {
		render.Status(r, http.StatusInternalServerError)
		render.JSON(w, r, map[string]string{
			"error": "Failed to fetch tracks",
		})
		return
	}

	render.JSON(w, r, tracks)
}

func (h *Handler) GetArtist(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validID(id) {
		http.Error(w, "id must be a valid UUID", http.StatusBadRequest)
		return
	}

	artist, err := h.Service.GetArtist(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	render.JSON(w, r, artist)
}

func (h *Handler) PatchArtist(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validID(id) {
		http.Error(w, "id must be a valid UUID", http.StatusBadRequest)
		return
	}

	defer r.Body.Close()

	var payload ArtistPayload

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(payload.Name) == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	err := h.Service.PatchArtist(r.Context(), id, payload.Name)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	render.JSON(w, r, map[string]string{
		"message": "Success",
	})
}

func (h *Handler) DeleteArtist(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validID(id) {
		http.Error(w, "id must be a valid UUID", http.StatusBadRequest)
		return
	}

	if err := h.Service.DeleteArtist(r.Context(), id); err != nil {
		writeServiceError(w, err)
		return
	}

	render.JSON(w, r, Response{Message: "Artist deleted"})
}

func (h *Handler) GetArtistAvatar(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validID(id) {
		http.Error(w, "id must be a valid UUID", http.StatusBadRequest)
		return
	}

	object, err := h.Service.GetArtistAvatarObject(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	h.ServeObject(w, r, object)
}

func (h *Handler) PutArtistAvatar(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validID(id) {
		http.Error(w, "id must be a valid UUID", http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	if err := r.ParseMultipartForm(maxCoverSize); err != nil {
		http.Error(w, "avatar must be valid multipart data up to 15 MiB", http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()

	avatar, header, err := r.FormFile("avatar")
	if err != nil {
		http.Error(w, "avatar is required", http.StatusBadRequest)
		return
	}
	defer avatar.Close()

	if err := h.Service.UploadArtistAvatar(r.Context(), id, avatar, header.Size); err != nil {
		writeServiceError(w, err)
		return
	}

	render.JSON(w, r, Response{Message: "Artist avatar updated"})
}

func (h *Handler) DeleteArtistAvatar(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validID(id) {
		http.Error(w, "id must be a valid UUID", http.StatusBadRequest)
		return
	}

	if err := h.Service.DeleteArtistAvatar(r.Context(), id); err != nil {
		writeServiceError(w, err)
		return
	}

	render.JSON(w, r, Response{Message: "Artist avatar removed"})
}

func (h *Handler) StreamTrack(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	object, err := h.Service.GetTrackObject(r.Context(), id)
	if err != nil {
		http.Error(w, "track not found", http.StatusNotFound)
		return
	}

	h.ServeObject(w, r, object)
}

func (h *Handler) GetTrackCover(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validID(id) {
		http.Error(w, "id must be a valid UUID", http.StatusBadRequest)
		return
	}

	object, err := h.Service.GetTrackCoverObject(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	h.ServeObject(w, r, object)
}

func (h *Handler) GetAlbumCover(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	object, err := h.Service.GetAlbumCoverObject(r.Context(), id)
	if err != nil {
		http.Error(w, "cover not found", http.StatusNotFound)
		return
	}

	h.ServeObject(w, r, object)
}

func (h *Handler) ServeObject(w http.ResponseWriter, r *http.Request, object string) {
	file, info, err := h.Service.OpenObject(r.Context(), object)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	defer file.Close()

	if validUploadType(info.ContentType, "audio") || validUploadType(info.ContentType, "image") {
		w.Header().Set("Content-Type", info.ContentType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")

	http.ServeContent(w, r, filepath.Base(info.Key), info.LastModified, file)
}
