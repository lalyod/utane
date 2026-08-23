package app

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"

	"github.com/lalyod/utane/internal/db"
	minioClient "github.com/lalyod/utane/internal/minio"
	"github.com/lalyod/utane/internal/music"
)

func Serve() error {
	_ = godotenv.Load()

	dbPoll, err := db.New(context.Background())
	if err != nil {
		return err
	}
	defer dbPoll.Close()

	minio := minioClient.New(
		os.Getenv("MINIO_ENDPOINT"),
		os.Getenv("MINIO_ACCESS_KEY"),
		os.Getenv("MINIO_SECRET_KEY"),
		os.Getenv("MINIO_BUCKET"),
		os.Getenv("MINIO_USE_SSL") == "true",
	)

	minio.EnsureBucket(context.Background())

	service := &music.Service{
		Minio:               minio,
		DB:                  dbPoll,
		HTTPClient:          &http.Client{Timeout: 10 * time.Second},
		LyricsEndpoint:      "https://lrclib.net/api/get",
		MusicBrainzEndpoint: "https://musicbrainz.org/ws/2/release/",
		CoverArtEndpoint:    "https://coverartarchive.org",
		MusicBrainzInterval: time.Second,
	}
	handler := &music.Handler{Service: service}

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
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join("web", "index.html"))
	})
	r.Get("/manage", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join("web", "index.html"))
	})
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
	r.Mount("/music", handler.Routes())

	log.Println("Server running on :8080")

	return http.ListenAndServe(":8080", r)
}
