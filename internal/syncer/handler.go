package syncer

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
)

var groupCodePattern = regexp.MustCompile(`^[A-Za-z0-9]{4,32}$`)

// Routes is the UI-facing API, mounted on the loopback server only.
func (s *Syncer) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/status", s.getStatus)
	r.Put("/config", s.putConfig)
	r.Post("/run", func(w http.ResponseWriter, _ *http.Request) {
		s.Trigger()
		w.WriteHeader(http.StatusAccepted)
	})

	return r
}

type statusResponse struct {
	Config Config `json:"config"`
	Status Status `json:"status"`
}

func (s *Syncer) getStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(statusResponse{Config: s.Config(), Status: s.Status()})
}

func (s *Syncer) putConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Folder    string `json:"folder"`
		LAN       bool   `json:"lan"`
		GroupCode string `json:"groupCode"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	body.Folder = strings.TrimSpace(body.Folder)
	body.GroupCode = strings.ToUpper(strings.TrimSpace(body.GroupCode))
	if body.Folder != "" {
		if info, err := os.Stat(body.Folder); err != nil || !info.IsDir() {
			http.Error(w, "folder does not exist", http.StatusBadRequest)
			return
		}
	}
	if body.GroupCode != "" && !groupCodePattern.MatchString(body.GroupCode) {
		http.Error(w, "group code must be 4-32 letters or digits", http.StatusBadRequest)
		return
	}
	if err := s.SetConfig(body.Folder, body.LAN, body.GroupCode); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.getStatus(w, r)
}
