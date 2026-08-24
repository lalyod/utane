package music

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
)

type TrackResponse struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

type LyricsPayload struct {
	Lyrics string `json:"lyrics"`
}

type CoverFetchPayload struct {
	ReleaseID string `json:"release_id"`
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
