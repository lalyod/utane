package music

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
)

type ArtistPayload struct {
	Name string `json:"name"`
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
