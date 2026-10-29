package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/lalyod/utane/internal/db"
	"github.com/lalyod/utane/internal/media"
	"github.com/lalyod/utane/internal/music"
	"github.com/lalyod/utane/internal/syncer"
	"github.com/lalyod/utane/migrations"
	"github.com/lalyod/utane/web"
)

const defaultAddr = "127.0.0.1:8080"

// dataDir is where the database and media live: UTANE_DATA, else the user config directory.
func dataDir() (string, error) {
	if dir := os.Getenv("UTANE_DATA"); dir != "" {
		return dir, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "Utane"), nil
}

// listenAddr reads UTANE_ADDR and refuses non-loopback hosts: the app has no authentication.
func listenAddr() (string, error) {
	addr := os.Getenv("UTANE_ADDR")
	if addr == "" {
		return defaultAddr, nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("UTANE_ADDR: %w", err)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", fmt.Errorf("UTANE_ADDR %q is not a loopback address", addr)
	}

	return addr, nil
}

func Serve() error {
	addr, err := listenAddr()
	if err != nil {
		return err
	}
	dir, err := dataDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	conn, err := db.Open(filepath.Join(dir, "utane.db"))
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := migrateUp(context.Background(), conn, migrations.FS); err != nil {
		return err
	}

	store, err := media.New(filepath.Join(dir, "media"))
	if err != nil {
		return err
	}
	defer store.Close()

	service := &music.Service{
		Media:               store,
		DB:                  conn,
		HTTPClient:          &http.Client{Timeout: 30 * time.Second},
		LyricsEndpoint:      "https://lrclib.net/api/get",
		MusicBrainzEndpoint: "https://musicbrainz.org/ws/2/release/",
		CoverArtEndpoint:    "https://coverartarchive.org",
		MusicBrainzInterval: time.Second,
	}
	handler := &music.Handler{Service: service}

	devices, err := syncer.New(conn, store, dir)
	if err != nil {
		return err
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go devices.Run(ctx)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			defer func() {
				slog.Info("HTTP request",
					"method", r.Method,
					"path", r.URL.Path,
					"status", ww.Status(),
					"bytes", ww.BytesWritten(),
					"duration", time.Since(start),
				)
			}()

			next.ServeHTTP(ww, r)
		})
	})
	index := func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, web.FS, "index.html")
	}
	r.Get("/", index)
	r.Get("/manage", index)
	r.Get("/playlists", index)
	r.Handle("/static/*", http.FileServerFS(web.FS))
	r.Get("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, web.FS, "manifest.json")
	})
	r.Get("/sw.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Service-Worker-Allowed", "/")
		http.ServeFileFS(w, r, web.FS, "sw.js")
	})
	r.Mount("/music", handler.Routes())
	r.Mount("/sync", devices.Routes())

	log.Println("Server running on " + addr)

	if err := http.ListenAndServe(addr, r); !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}
