package music

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/lalyod/utane/internal/db"
	"github.com/lalyod/utane/internal/media"
	"github.com/lalyod/utane/migrations"
)

func newTestService(t *testing.T) *Service {
	t.Helper()

	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)

	schema, err := migrations.FS.ReadFile("001_init.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(context.Background(), string(schema)); err != nil {
		t.Fatal(err)
	}

	store, err := media.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	return &Service{DB: conn, Media: store}
}

func TestLibraryRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)

	if err := s.CreateArtist(ctx, "Artist"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateArtist(ctx, "Artist"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate artist error = %v, want ErrConflict", err)
	}
	artists, err := s.GetArtists(ctx)
	if err != nil || len(artists) != 1 {
		t.Fatalf("artists = %v, %v", artists, err)
	}
	artistID := artists[0].ID.String()
	if artists[0].CreatedAt.IsZero() {
		t.Fatal("created_at was not scanned into a time")
	}

	if err := s.CreateAlbum(ctx, artistID, "Album", nil, ""); err != nil {
		t.Fatal(err)
	}
	albums, err := s.GetAlbums(ctx)
	if err != nil || len(albums) != 1 {
		t.Fatalf("albums = %v, %v", albums, err)
	}
	albumID := albums[0].ID.String()

	object := "tracks/" + uuid.NewString() + ".mp3"
	if err := s.Media.Put(object, strings.NewReader("audio")); err != nil {
		t.Fatal(err)
	}
	two := 2
	trackID, err := s.CreateTrack(ctx, artistID, albumID, "Song", 180, &two, object)
	if err != nil {
		t.Fatal(err)
	}
	tracks, err := s.GetTracks(ctx)
	if err != nil || len(tracks) != 1 || tracks[0].AlbumID != albumID || tracks[0].TrackNumber != 2 {
		t.Fatalf("tracks = %+v, %v", tracks, err)
	}

	otherObject := "tracks/" + uuid.NewString() + ".mp3"
	if err := s.Media.Put(otherObject, strings.NewReader("audio")); err != nil {
		t.Fatal(err)
	}
	otherID, err := s.CreateTrack(ctx, artistID, "", "Other", 60, nil, otherObject)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CreatePlaylist(ctx, "Mix"); err != nil {
		t.Fatal(err)
	}
	playlists, err := s.GetPlaylists(ctx)
	if err != nil || len(playlists) != 1 || len(playlists[0].TrackIDs) != 0 {
		t.Fatalf("empty playlist = %+v, %v", playlists, err)
	}
	playlistID := playlists[0].ID.String()
	for _, id := range []string{otherID, trackID, otherID} {
		if err := s.AddPlaylistTrack(ctx, playlistID, id); err != nil {
			t.Fatal(err)
		}
	}
	playlists, _ = s.GetPlaylists(ctx)
	if got := playlists[0].TrackIDs; len(got) != 2 || got[0] != otherID || got[1] != trackID {
		t.Fatalf("playlist order = %v", got)
	}

	if err := s.DeleteAlbum(ctx, albumID); err != nil {
		t.Fatal(err)
	}
	tracks, _ = s.GetTracks(ctx)
	if tracks[0].AlbumID != "" && tracks[1].AlbumID != "" {
		t.Fatal("deleting an album should detach its tracks")
	}

	if err := s.DeleteTrack(ctx, trackID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.OpenObject(ctx, object); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted track file error = %v, want ErrNotFound", err)
	}
	playlists, _ = s.GetPlaylists(ctx)
	if got := playlists[0].TrackIDs; len(got) != 1 || got[0] != otherID {
		t.Fatalf("playlist after track delete = %v", got)
	}
	if err := s.DeleteTrack(ctx, trackID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete error = %v, want ErrNotFound", err)
	}
}
