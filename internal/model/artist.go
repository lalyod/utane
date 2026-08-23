package model

import (
	"time"

	"github.com/google/uuid"
)

type Artist struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	HasAvatar bool      `json:"has_avatar"`
	CreatedAt time.Time `json:"created_at"`
}

type Album struct {
	ID         uuid.UUID `json:"id"`
	ArtistID   uuid.UUID `json:"artist_id"`
	ArtistName string    `json:"artist_name"`
	Title      string    `json:"title"`
	HasCover   bool      `json:"has_cover"`
	Year       int       `json:"year,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type Track struct {
	ID           uuid.UUID `json:"id"`
	ArtistID     uuid.UUID `json:"artist_id"`
	ArtistName   string    `json:"artist_name"`
	AlbumID      string    `json:"album_id,omitempty"`
	AlbumTitle   string    `json:"album_title,omitempty"`
	Title        string    `json:"title"`
	Duration     int       `json:"duration"`
	TrackNumber  int       `json:"track_number,omitempty"`
	HasCover     bool      `json:"has_cover"`
	SyncedLyrics string    `json:"synced_lyrics,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type Playlist struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	TrackIDs  []string  `json:"track_ids"`
	CreatedAt time.Time `json:"created_at"`
}
