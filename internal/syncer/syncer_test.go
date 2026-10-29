package syncer

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lalyod/utane/internal/db"
	"github.com/lalyod/utane/internal/media"
	"github.com/lalyod/utane/migrations"
)

type node struct {
	t      *testing.T
	conn   *db.DB
	store  *media.Store
	merger *Merger
	syncer *Syncer
}

func newNode(t *testing.T) *node {
	t.Helper()
	dir := t.TempDir()

	conn, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	for _, name := range []string{"001_init.up.sql", "002_sync.up.sql", "003_plays.up.sql"} {
		schema, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(context.Background(), string(schema)); err != nil {
			t.Fatal(name, err)
		}
	}
	store, err := media.New(filepath.Join(dir, "media"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	s, err := New(conn, store, dir)
	if err != nil {
		t.Fatal(err)
	}

	return &node{t: t, conn: conn, store: store, merger: &Merger{DB: conn, Media: store}, syncer: s}
}

func (n *node) exec(query string, args ...any) {
	n.t.Helper()
	if _, err := n.conn.Exec(context.Background(), query, args...); err != nil {
		n.t.Fatal(err)
	}
}

func (n *node) count(query string, args ...any) int {
	n.t.Helper()
	var count int
	if err := n.conn.QueryRow(context.Background(), query, args...).Scan(&count); err != nil {
		n.t.Fatal(err)
	}

	return count
}

func (n *node) text(query string, args ...any) string {
	n.t.Helper()
	var text string
	if err := n.conn.QueryRow(context.Background(), query, args...).Scan(&text); err != nil {
		n.t.Fatal(err)
	}

	return text
}

func (n *node) hasFile(name string) bool {
	file, err := n.store.Open(name)
	if err == nil {
		file.Close()
	}

	return err == nil
}

// source lets another node read this one in-process.
func (n *node) source() Source { return nodeSource{n} }

type nodeSource struct{ n *node }

func (s nodeSource) Snapshot(ctx context.Context) (*Snapshot, error) {
	return Export(ctx, s.n.conn, "peer")
}

func (s nodeSource) Fetch(_ context.Context, name string) (io.ReadCloser, error) {
	return s.n.store.Open(name)
}

func (n *node) mergeFrom(other *node) int {
	n.t.Helper()
	changed, err := n.merger.Merge(context.Background(), other.source())
	if err != nil {
		n.t.Fatal(err)
	}

	return changed
}

func tick() { time.Sleep(5 * time.Millisecond) } // updated_at is in ms; keep edits strictly ordered

const (
	artistID   = "11111111-1111-4111-8111-111111111111"
	albumID    = "22222222-2222-4222-8222-222222222222"
	trackID    = "33333333-3333-4333-8333-333333333333"
	track2ID   = "44444444-4444-4444-8444-444444444444"
	playlistID = "55555555-5555-4555-8555-555555555555"
)

func (n *node) seed() {
	n.t.Helper()
	n.exec(`INSERT INTO artists (id, name) VALUES (?1, 'Queen')`, artistID)
	n.exec(`INSERT INTO albums (id, artist_id, title) VALUES (?1, ?2, 'Jazz')`, albumID, artistID)
	for id, title := range map[string]string{trackID: "Mustapha", track2ID: "Fat Bottomed Girls"} {
		object := "tracks/" + id + ".mp3"
		if err := n.store.Put(object, strings.NewReader("audio "+title)); err != nil {
			n.t.Fatal(err)
		}
		n.exec(`INSERT INTO tracks (id, artist_id, album_id, title, duration, object_path) VALUES (?1, ?2, ?3, ?4, 180, ?5)`,
			id, artistID, albumID, title, object)
	}
	n.exec(`INSERT INTO playlists (id, name) VALUES (?1, 'Mix')`, playlistID)
	n.exec(`INSERT INTO playlist_tracks (playlist_id, track_id, position) VALUES (?1, ?2, 1), (?1, ?3, 2)`,
		playlistID, track2ID, trackID)
}

func TestMergeCopiesLibraryAndMedia(t *testing.T) {
	a, b := newNode(t), newNode(t)
	a.seed()

	if changed := b.mergeFrom(a); changed == 0 {
		t.Fatal("first merge changed nothing")
	}

	if got := b.count(`SELECT count(*) FROM tracks`); got != 2 {
		t.Fatalf("tracks = %d, want 2", got)
	}
	if !b.hasFile("tracks/" + trackID + ".mp3") {
		t.Fatal("audio file was not copied")
	}
	if got := b.text(`SELECT group_concat(track_id) FROM (SELECT track_id FROM playlist_tracks ORDER BY position)`); got != track2ID+","+trackID {
		t.Fatalf("playlist order = %s", got)
	}
	if b.count(`SELECT count(*) FROM tombstones`) != 0 {
		t.Fatal("merge created tombstones")
	}
	if changed := b.mergeFrom(a); changed != 0 {
		t.Fatalf("second merge changed %d rows, want 0", changed)
	}
	// Merging must not bump timestamps, or devices would trade the same rows forever.
	if changed := a.mergeFrom(b); changed != 0 {
		t.Fatalf("merge back changed %d rows, want 0", changed)
	}
}

func TestLastWriterWins(t *testing.T) {
	a, b := newNode(t), newNode(t)
	a.seed()
	b.mergeFrom(a)

	tick()
	b.exec(`UPDATE tracks SET title = 'Mustapha (Remastered)' WHERE id = ?1`, trackID)
	tick()
	a.exec(`UPDATE tracks SET title = 'Older edit' WHERE id = ?1`, track2ID)

	a.mergeFrom(b)
	b.mergeFrom(a)

	for _, n := range []*node{a, b} {
		if got := n.text(`SELECT title FROM tracks WHERE id = ?1`, trackID); got != "Mustapha (Remastered)" {
			t.Fatalf("track title = %q", got)
		}
		if got := n.text(`SELECT title FROM tracks WHERE id = ?1`, track2ID); got != "Older edit" {
			t.Fatalf("track2 title = %q", got)
		}
	}
}

func TestDeletePropagates(t *testing.T) {
	a, b := newNode(t), newNode(t)
	a.seed()
	b.mergeFrom(a)

	tick()
	a.exec(`DELETE FROM tracks WHERE id = ?1`, trackID)
	if err := a.store.Remove("tracks/" + trackID + ".mp3"); err != nil {
		t.Fatal(err)
	}
	b.mergeFrom(a)

	if b.count(`SELECT count(*) FROM tracks WHERE id = ?1`, trackID) != 0 {
		t.Fatal("deleted track still on b")
	}
	if b.hasFile("tracks/" + trackID + ".mp3") {
		t.Fatal("deleted track's file still on b")
	}
	if got := b.count(`SELECT count(*) FROM playlist_tracks`); got != 1 {
		t.Fatalf("playlist_tracks = %d, want 1", got)
	}

	// A stale device must not resurrect it.
	c := newNode(t)
	c.mergeFrom(a)
	if c.count(`SELECT count(*) FROM tracks WHERE id = ?1`, trackID) != 0 {
		t.Fatal("deleted track reached a fresh device")
	}
}

func TestDeleteCascadesThroughArtist(t *testing.T) {
	a, b := newNode(t), newNode(t)
	a.seed()
	b.mergeFrom(a)

	tick()
	a.exec(`DELETE FROM artists WHERE id = ?1`, artistID)
	b.mergeFrom(a)

	if got := b.count(`SELECT (SELECT count(*) FROM artists) + (SELECT count(*) FROM albums) + (SELECT count(*) FROM tracks)`); got != 0 {
		t.Fatalf("rows left after artist delete = %d", got)
	}
}

func TestSameNameCreatedOnBothDevicesIsNotDuplicated(t *testing.T) {
	a, b := newNode(t), newNode(t)
	a.exec(`INSERT INTO artists (id, name) VALUES (?1, 'Queen')`, artistID)
	a.exec(`INSERT INTO playlists (id, name) VALUES (?1, 'Mix')`, playlistID)
	b.exec(`INSERT INTO artists (id, name) VALUES ('66666666-6666-4666-8666-666666666666', 'Queen')`)
	b.exec(`INSERT INTO playlists (id, name) VALUES ('77777777-7777-4777-8777-777777777777', 'Mix')`)

	a.mergeFrom(b)
	b.mergeFrom(a)

	for _, n := range []*node{a, b} {
		if got := n.count(`SELECT count(*) FROM artists`); got != 1 {
			t.Fatalf("artists = %d, want 1", got)
		}
		if got := n.count(`SELECT count(*) FROM playlists`); got != 1 {
			t.Fatalf("playlists = %d, want 1", got)
		}
	}
}

func TestMissingMediaSkipsRowUntilItArrives(t *testing.T) {
	a, b := newNode(t), newNode(t)
	a.seed()
	if err := a.store.Remove("tracks/" + trackID + ".mp3"); err != nil {
		t.Fatal(err)
	}

	if _, err := b.merger.Merge(context.Background(), a.source()); err == nil {
		t.Fatal("merge with a missing file reported no error")
	}
	if got := b.count(`SELECT count(*) FROM tracks`); got != 1 {
		t.Fatalf("tracks = %d, want only the one with its file", got)
	}
	// The playlist needs both tracks, so it waits too rather than landing with a hole.
	if got := b.count(`SELECT count(*) FROM playlists`); got != 0 {
		t.Fatalf("playlist applied with a missing member")
	}

	if err := a.store.Put("tracks/"+trackID+".mp3", strings.NewReader("audio")); err != nil {
		t.Fatal(err)
	}
	b.mergeFrom(a)
	if got := b.count(`SELECT count(*) FROM tracks`); got != 2 {
		t.Fatalf("tracks after retry = %d, want 2", got)
	}
	if got := b.count(`SELECT count(*) FROM playlist_tracks`); got != 2 {
		t.Fatalf("playlist_tracks after retry = %d, want 2", got)
	}
}

func TestFolderRoundTrip(t *testing.T) {
	ctx := context.Background()
	a, b := newNode(t), newNode(t)
	a.seed()
	folder := t.TempDir()

	if err := a.syncer.pushFolder(ctx, folder, "device-a"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"device-a.json", filepath.Join("media", "tracks", trackID+".mp3")} {
		if _, err := os.Stat(filepath.Join(folder, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(folder, "device-a.json.tmp")); !os.IsNotExist(err) {
		t.Fatal("temp snapshot left behind")
	}

	// A device never merges its own file.
	if changed, err := a.syncer.pullFolder(ctx, folder, "device-a"); err != nil || changed != 0 {
		t.Fatalf("own file: changed=%d err=%v", changed, err)
	}
	changed, err := b.syncer.pullFolder(ctx, folder, "device-b")
	if err != nil || changed == 0 {
		t.Fatalf("pull: changed=%d err=%v", changed, err)
	}
	if got := b.count(`SELECT count(*) FROM tracks`); got != 2 || !b.hasFile("tracks/"+trackID+".mp3") {
		t.Fatalf("tracks = %d after folder pull", got)
	}
}

func TestFolderRejectsEscapingMediaName(t *testing.T) {
	folder := t.TempDir()
	if err := os.WriteFile(filepath.Join(folder, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(folder, "media"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := &folderSource{dir: folder}
	if file, err := src.Fetch(context.Background(), "../secret.txt"); err == nil {
		file.Close()
		t.Fatal("fetched a file outside media/")
	}
}

func TestLANHandlerRequiresGroupCode(t *testing.T) {
	a, b := newNode(t), newNode(t)
	a.seed()
	server := httptest.NewServer(a.syncer.lanHandler())
	defer server.Close()

	resp, err := http.Get(server.URL + "/sync/snapshot")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no code: status %d", resp.StatusCode)
	}

	wrong := &lanSource{base: server.URL, code: "WRONGCODE", client: server.Client()}
	if _, err := wrong.Snapshot(context.Background()); err == nil {
		t.Fatal("wrong code accepted")
	}

	right := &lanSource{base: server.URL, code: a.syncer.Config().GroupCode, client: server.Client()}
	if changed, err := b.merger.Merge(context.Background(), right); err != nil || changed == 0 {
		t.Fatalf("merge over LAN: changed=%d err=%v", changed, err)
	}
	if got := b.count(`SELECT count(*) FROM tracks`); got != 2 || !b.hasFile("tracks/"+trackID+".mp3") {
		t.Fatalf("tracks = %d after LAN merge", got)
	}
	if _, err := right.Fetch(context.Background(), "../sync.json"); err == nil {
		t.Fatal("LAN media fetch escaped the media directory")
	}
}

func TestExportIsStableWhenNothingChanged(t *testing.T) {
	a := newNode(t)
	a.seed()
	one, err := Export(context.Background(), a.conn, "x")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	two, err := Export(context.Background(), a.conn, "x")
	if err != nil {
		t.Fatal(err)
	}
	if one.At != two.At {
		t.Fatal("snapshot time moved without a change")
	}
}

func TestPlaysSyncAndLongerListenWins(t *testing.T) {
	a, b := newNode(t), newNode(t)
	a.seed()
	b.mergeFrom(a)

	playID := "88888888-8888-4888-8888-888888888888"
	now := time.Now().UnixMilli()
	a.exec(`INSERT INTO plays (id, track_id, played_at, seconds, updated_at) VALUES (?1, ?2, ?3, 40, ?3)`, playID, trackID, now)

	if b.mergeFrom(a) == 0 {
		t.Fatal("play was not copied")
	}
	if got := b.count(`SELECT seconds FROM plays WHERE id = ?1`, playID); got != 40 {
		t.Fatalf("seconds = %d, want 40", got)
	}
	if changed := b.mergeFrom(a); changed != 0 {
		t.Fatalf("second merge changed %d rows, want 0", changed)
	}

	// A later checkpoint of the same listen extends it, and a shorter one never shrinks it.
	b.exec(`UPDATE plays SET seconds = 120, updated_at = updated_at + 1 WHERE id = ?1`, playID)
	a.mergeFrom(b)
	if got := a.count(`SELECT seconds FROM plays WHERE id = ?1`, playID); got != 120 {
		t.Fatalf("seconds after extend = %d, want 120", got)
	}
	a.exec(`UPDATE plays SET seconds = 60 WHERE id = ?1`, playID)
	if a.mergeFrom(b); a.count(`SELECT seconds FROM plays WHERE id = ?1`, playID) != 120 {
		t.Fatal("longer listen from peer did not win")
	}
}

func TestPlayOfDeletedTrackIsDropped(t *testing.T) {
	a, b := newNode(t), newNode(t)
	a.seed()
	b.mergeFrom(a)

	playID := "88888888-8888-4888-8888-888888888888"
	now := time.Now().UnixMilli()
	a.exec(`INSERT INTO plays (id, track_id, played_at, seconds, updated_at) VALUES (?1, ?2, ?3, 40, ?3)`, playID, trackID, now)
	b.exec(`DELETE FROM tracks WHERE id = ?1`, trackID)

	b.mergeFrom(a)
	if b.count(`SELECT count(*) FROM plays`) != 0 {
		t.Fatal("play of a deleted track was imported")
	}
}
