package music

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
)

type Handler struct {
	Service *Service
}

type Response struct {
	Message string `json:"message"`
}

type ArtistPayload struct {
	Name string `json:"name"`
}

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	// r.Get("{id}/stream", h.Stream)
	r.Post("/tracks", h.CreateTrack)

	r.Post("/artists", h.CreateArtist)
	r.Get("/artists", h.GetArtists)
	r.Get("/artists/{id}", h.GetArtist)
	r.Patch("/artists/{id}", h.PatchArtist)
	return r
}

func (h *Handler) CreateTrack(w http.ResponseWriter, r *http.Request) {
	audio, header, err := r.FormFile("audio")
	artistID := r.FormValue("id_artist")
	albumID := r.FormValue("id_album")
	title := r.FormValue("title")
	duration := r.FormValue("duration")

	object, err := h.Service.UploadTrack(r.Context(), audio, header)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = h.Service.CreateTrack(r.Context(), artistID, albumID, title, duration, object)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	response := &Response{
		Message: "Create track successfully",
	}

	render.JSON(w, r, response)
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
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusCreated)
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

func (h *Handler) GetArtist(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	artist, err := h.Service.GetArtist(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	render.JSON(w, r, artist)
}

func (h *Handler) PatchArtist(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

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
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	render.JSON(w, r, map[string]string{
		"message": "Success",
	})
}

// func (h *Handler) Stream(w http.ResponseWriter,r *http.Request){
// 	id := chi.URLParam(r, "id")
//
// 	var track
//
// 	url, err := h.Service.GetStreamURL(r.Context(), track.Object)
// 	if err != nil {
// 		http.Error(w, err.Error(), 500)
// 		return
// 	}
//
// 	json.NewEncoder(w).Encode(map[string]string{
// 		"url": url,
// 	})
// }
