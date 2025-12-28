package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/joho/godotenv"
	"github.com/lalyod/mysong/internal/db"
	minioClient "github.com/lalyod/mysong/internal/minio"
	"github.com/lalyod/mysong/internal/music"
)

func main() {
	godotenv.Load()

	dbPoll, err := db.New(context.Background())
	if err != nil {
		log.Fatal(err)
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
		Minio: minio,
		DB: dbPoll,
	}
	handler := &music.Handler{Service: service,}

	r := chi.NewRouter()
	r.Mount("/music", handler.Routes())

	log.Println("Server running on :8080")
	http.ListenAndServe(":8080", r)
}
