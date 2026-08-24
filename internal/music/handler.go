package music

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type Handler struct {
	Service *Service
}

type Response struct {
	Message string `json:"message"`
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
	r.Get("/playlists", h.GetPlaylists)

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
	r.Post("/playlists", h.CreatePlaylist)
	r.Patch("/playlists/{id}", h.PatchPlaylist)
	r.Delete("/playlists/{id}", h.DeletePlaylist)
	r.Post("/playlists/{id}/tracks", h.AddPlaylistTrack)
	r.Delete("/playlists/{id}/tracks/{trackID}", h.DeletePlaylistTrack)

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

func (h *Handler) ServeObject(w http.ResponseWriter, r *http.Request, object string) {
	file, info, err := h.Service.OpenObject(r.Context(), object)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	defer file.Close()

	if contentType := servedContentType(info.Key, info.ContentType); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")

	http.ServeContent(w, r, filepath.Base(info.Key), info.LastModified, file)
}

func servedContentType(name string, contentType string) string {
	if strings.EqualFold(filepath.Ext(name), ".m4a") || strings.EqualFold(contentType, "audio/x-m4a") {
		return "audio/mp4"
	}
	if validUploadType(contentType, "audio") || validUploadType(contentType, "image") {
		return contentType
	}

	return ""
}
