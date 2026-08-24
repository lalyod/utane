package music

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
)

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

func (h *Handler) GetAlbumCover(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	object, err := h.Service.GetAlbumCoverObject(r.Context(), id)
	if err != nil {
		http.Error(w, "cover not found", http.StatusNotFound)
		return
	}

	h.ServeObject(w, r, object)
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
