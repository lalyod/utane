package music

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
)

type PlaylistPayload struct {
	Name string `json:"name"`
}

type PlaylistTrackPayload struct {
	TrackID string `json:"track_id"`
}

func (h *Handler) GetPlaylists(w http.ResponseWriter, r *http.Request) {
	playlists, err := h.Service.GetPlaylists(r.Context())
	if err != nil {
		render.Status(r, http.StatusInternalServerError)
		render.JSON(w, r, map[string]string{"error": "Failed to fetch playlists"})
		return
	}

	render.JSON(w, r, playlists)
}

func (h *Handler) CreatePlaylist(w http.ResponseWriter, r *http.Request) {
	name, ok := decodePlaylistName(w, r)
	if !ok {
		return
	}

	if err := h.Service.CreatePlaylist(r.Context(), name); err != nil {
		writeServiceError(w, err)
		return
	}

	render.Status(r, http.StatusCreated)
	render.JSON(w, r, Response{Message: "Playlist created"})
}

func (h *Handler) PatchPlaylist(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validID(id) {
		http.Error(w, "id must be a valid UUID", http.StatusBadRequest)
		return
	}
	name, ok := decodePlaylistName(w, r)
	if !ok {
		return
	}

	if err := h.Service.PatchPlaylist(r.Context(), id, name); err != nil {
		writeServiceError(w, err)
		return
	}

	render.JSON(w, r, Response{Message: "Playlist renamed"})
}

func (h *Handler) DeletePlaylist(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validID(id) {
		http.Error(w, "id must be a valid UUID", http.StatusBadRequest)
		return
	}

	if err := h.Service.DeletePlaylist(r.Context(), id); err != nil {
		writeServiceError(w, err)
		return
	}

	render.JSON(w, r, Response{Message: "Playlist deleted"})
}

func (h *Handler) AddPlaylistTrack(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validID(id) {
		http.Error(w, "id must be a valid UUID", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var payload PlaylistTrackPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	trackID := strings.TrimSpace(payload.TrackID)
	if !validID(trackID) {
		http.Error(w, "track_id must be a valid UUID", http.StatusBadRequest)
		return
	}

	if err := h.Service.AddPlaylistTrack(r.Context(), id, trackID); err != nil {
		writeServiceError(w, err)
		return
	}

	render.Status(r, http.StatusCreated)
	render.JSON(w, r, Response{Message: "Track added to playlist"})
}

func (h *Handler) DeletePlaylistTrack(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	trackID := chi.URLParam(r, "trackID")
	if !validID(id) || !validID(trackID) {
		http.Error(w, "id and trackID must be valid UUIDs", http.StatusBadRequest)
		return
	}

	if err := h.Service.DeletePlaylistTrack(r.Context(), id, trackID); err != nil {
		writeServiceError(w, err)
		return
	}

	render.JSON(w, r, Response{Message: "Track removed from playlist"})
}

func decodePlaylistName(w http.ResponseWriter, r *http.Request) (string, bool) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var payload PlaylistPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", false
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return "", false
	}

	return name, true
}
