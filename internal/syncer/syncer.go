package syncer

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/lalyod/utane/internal/db"
	"github.com/lalyod/utane/internal/media"
)

const interval = time.Minute

type Status struct {
	Syncing   bool   `json:"syncing"`
	LastRun   int64  `json:"lastRun"` // unix ms, 0 = never
	LastError string `json:"lastError"`
	// Changed counts rows merged from other devices since start; the UI reloads when it moves.
	Changed int64 `json:"changed"`
	Peers   int   `json:"peers"`
}

type Syncer struct {
	DB     *db.DB
	Media  *media.Store
	Dir    string // where sync.json lives
	Client *http.Client

	merger  *Merger
	trigger chan struct{}
	round   sync.Mutex // one sync round at a time

	mu     sync.Mutex // guards cfg and status
	cfg    Config
	status Status

	lanMu   sync.Mutex
	lan     *lanService
	peersMu sync.Mutex
	peers   map[string]peer
}

func New(conn *db.DB, store *media.Store, dir string) (*Syncer, error) {
	cfg, err := loadConfig(dir)
	if err != nil {
		return nil, err
	}

	return &Syncer{
		DB:      conn,
		Media:   store,
		Dir:     dir,
		Client:  &http.Client{Timeout: 10 * time.Minute},
		merger:  &Merger{DB: conn, Media: store},
		trigger: make(chan struct{}, 1),
		cfg:     cfg,
		peers:   map[string]peer{},
	}, nil
}

func (s *Syncer) Config() Config {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.cfg
}

func (s *Syncer) Status() Status {
	s.mu.Lock()
	status := s.status
	s.mu.Unlock()
	status.Peers = len(s.livePeers())

	return status
}

// SetConfig stores the user-editable fields and applies them.
func (s *Syncer) SetConfig(folder string, lan bool, groupCode string) error {
	s.mu.Lock()
	s.cfg.Folder = folder
	s.cfg.LAN = lan
	if groupCode != "" {
		s.cfg.GroupCode = groupCode
	}
	cfg := s.cfg
	s.mu.Unlock()

	if err := saveConfig(s.Dir, cfg); err != nil {
		return err
	}
	if err := s.applyLAN(); err != nil {
		return err
	}
	s.Trigger()

	return nil
}

// Trigger asks Run for a round now; a round already queued covers it.
func (s *Syncer) Trigger() {
	select {
	case s.trigger <- struct{}{}:
	default:
	}
}

// Run syncs at startup, every minute, and whenever triggered, until ctx ends.
func (s *Syncer) Run(ctx context.Context) {
	if err := s.applyLAN(); err != nil {
		slog.Warn("sync LAN listener", "error", err)
		s.setError(err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		s.Sync(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.trigger:
		}
	}
}

func (s *Syncer) setError(err error) {
	s.mu.Lock()
	s.status.LastError = err.Error()
	s.mu.Unlock()
}

// Sync runs one round over every configured transport: pull, then push so the folder carries what
// was just merged.
func (s *Syncer) Sync(ctx context.Context) {
	s.round.Lock()
	defer s.round.Unlock()

	s.mu.Lock()
	s.status.Syncing = true
	cfg := s.cfg
	s.mu.Unlock()

	changed := 0
	var problems []error
	if cfg.Folder != "" {
		n, err := s.pullFolder(ctx, cfg.Folder, cfg.DeviceID)
		changed += n
		if err != nil {
			problems = append(problems, err)
		}
		if err := s.pushFolder(ctx, cfg.Folder, cfg.DeviceID); err != nil {
			problems = append(problems, err)
		}
	}
	if cfg.LAN {
		n, err := s.pullPeers(ctx)
		changed += n
		if err != nil {
			problems = append(problems, err)
		}
	}

	err := errors.Join(problems...)
	if err != nil {
		slog.Warn("sync round", "error", err)
	}
	s.mu.Lock()
	s.status.Syncing = false
	s.status.LastRun = time.Now().UnixMilli()
	s.status.Changed += int64(changed)
	s.status.LastError = ""
	if err != nil {
		s.status.LastError = err.Error()
	}
	s.mu.Unlock()
}
