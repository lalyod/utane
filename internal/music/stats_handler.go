package music

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
)

type PlayPayload struct {
	TrackID string `json:"track_id"`
	Seconds int    `json:"seconds"`
}

func (h *Handler) GetHome(w http.ResponseWriter, r *http.Request) {
	home, err := h.Service.Home(r.Context(), time.Now())
	if err != nil {
		render.Status(r, http.StatusInternalServerError)
		render.JSON(w, r, map[string]string{"error": "Failed to compute home"})
		return
	}

	render.JSON(w, r, home)
}

// PutPlay records or extends one listen; the client picks the id so checkpoints are idempotent.
func (h *Handler) PutPlay(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validID(id) {
		http.Error(w, "id must be a valid UUID", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	defer r.Body.Close()

	var payload PlayPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "play must be valid JSON", http.StatusBadRequest)
		return
	}
	if !validID(payload.TrackID) {
		http.Error(w, "track_id must be a valid UUID", http.StatusBadRequest)
		return
	}

	if err := h.Service.RecordPlay(r.Context(), id, payload.TrackID, payload.Seconds, time.Now()); err != nil {
		writeServiceError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
