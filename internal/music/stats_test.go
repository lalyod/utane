package music

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lalyod/utane/internal/model"
	"github.com/lalyod/utane/migrations"
)

var statsNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

type library struct {
	artists   []model.Artist
	tracks    []model.Track
	playlists []model.Playlist
}

func (l *library) artist(name string) uuid.UUID {
	a := model.Artist{ID: uuid.New(), Name: name}
	l.artists = append(l.artists, a)

	return a.ID
}

func (l *library) track(artist uuid.UUID, title string, album string) string {
	t := model.Track{ID: uuid.New(), ArtistID: artist, Title: title, Duration: 200, AlbumID: album,
		CreatedAt: statsNow.Add(-time.Duration(len(l.tracks)) * time.Hour)}
	l.tracks = append(l.tracks, t)

	return t.ID.String()
}

func (l *library) home(plays []Play) *Home {
	return ComputeHome(l.artists, l.tracks, l.playlists, plays, statsNow)
}

// playAgo builds a play that started `ago` before statsNow.
func playAgo(track string, ago time.Duration, seconds int) Play {
	return Play{TrackID: track, PlayedAt: statsNow.Add(-ago).UnixMilli(), Seconds: seconds}
}

func ids(artists []HomeArtist) []string {
	out := make([]string, len(artists))
	for i, a := range artists {
		out[i] = a.ID
	}

	return out
}

func TestFavoriteArtistsPreferRecentOverOld(t *testing.T) {
	var l library
	recent, old := l.artist("Recent"), l.artist("Old")
	r, o := l.track(recent, "r", ""), l.track(old, "o", "")

	var plays []Play
	for i := 0; i < 3; i++ { // 3 listens yesterday vs 4 listens 150 days ago
		plays = append(plays, playAgo(r, 24*time.Hour+time.Duration(i)*time.Hour, 200))
	}
	for i := 0; i < 4; i++ {
		plays = append(plays, playAgo(o, 150*24*time.Hour+time.Duration(i)*time.Hour, 200))
	}

	got := ids(l.home(plays).Artists)
	if len(got) != 2 || got[0] != recent.String() {
		t.Fatalf("favorites = %v, want %s first", got, recent)
	}
}

func TestSkipsDoNotMakeAFavorite(t *testing.T) {
	var l library
	skipped, loved := l.artist("Skipped"), l.artist("Loved")
	s, v := l.track(skipped, "s", ""), l.track(loved, "v", "")

	var plays []Play
	for i := 0; i < 10; i++ {
		plays = append(plays, playAgo(s, time.Duration(i+1)*time.Hour, 6)) // 3% of the track
	}
	plays = append(plays, playAgo(v, 30*time.Minute, 190))

	artists := l.home(plays).Artists
	got := ids(artists)
	if len(got) != 2 || got[0] != loved.String() || got[1] != skipped.String() {
		t.Fatalf("favorites = %v, want %s then %s", got, loved, skipped)
	}
	if artists[1].Plays != 0 {
		t.Fatalf("skipped artist has %d counted plays, want 0", artists[1].Plays)
	}
}

func TestOneLoopedSongDoesNotBeatBreadth(t *testing.T) {
	var l library
	loop, broad := l.artist("Loop"), l.artist("Broad")
	one := l.track(loop, "one", "")
	var broadTracks []string
	for i := 0; i < 5; i++ {
		broadTracks = append(broadTracks, l.track(broad, fmt.Sprint("b", i), ""))
	}

	var plays []Play
	for i := 0; i < 8; i++ {
		plays = append(plays, playAgo(one, time.Duration(i+1)*time.Hour, 200))
	}
	for i, id := range broadTracks {
		for j := 0; j < 2; j++ {
			plays = append(plays, playAgo(id, time.Duration(i*2+j+1)*time.Hour, 200))
		}
	}

	if got := ids(l.home(plays).Artists); got[0] != broad.String() {
		t.Fatalf("favorites = %v, want %s first", got, broad)
	}
}

func TestMixFollowsListeningSessions(t *testing.T) {
	var l library
	seed, partner, stranger := l.artist("Seed"), l.artist("Partner"), l.artist("Stranger")
	var seedTracks, partnerTracks, strangerTracks []string
	for i := 0; i < 6; i++ {
		seedTracks = append(seedTracks, l.track(seed, fmt.Sprint("s", i), ""))
		partnerTracks = append(partnerTracks, l.track(partner, fmt.Sprint("p", i), ""))
		strangerTracks = append(strangerTracks, l.track(stranger, fmt.Sprint("x", i), ""))
	}

	// Seed and Partner are always heard in the same evening; Stranger gets its own days.
	var plays []Play
	for day := 1; day <= 5; day++ {
		start := time.Duration(day) * 24 * time.Hour
		plays = append(plays,
			playAgo(seedTracks[day%6], start, 200),
			playAgo(partnerTracks[day%6], start-5*time.Minute, 200),
			playAgo(strangerTracks[day%6], start+12*time.Hour, 200),
		)
	}
	// Make Seed the clear favorite so it is the first mix.
	for i := 0; i < 6; i++ {
		plays = append(plays, playAgo(seedTracks[i], time.Duration(i+1)*time.Hour, 200))
	}

	mix := l.home(plays).Mixes[0]
	if mix.SeedArtistID != seed.String() {
		t.Fatalf("first mix seed = %s, want %s", mix.SeedArtistID, seed)
	}
	byArtist := map[string]int{}
	for _, id := range mix.TrackIDs {
		for _, track := range l.tracks {
			if track.ID.String() == id {
				byArtist[track.ArtistID.String()]++
			}
		}
	}
	if byArtist[partner.String()] == 0 {
		t.Fatalf("co-listened artist missing from mix: %v", byArtist)
	}
	if byArtist[stranger.String()] != 0 {
		t.Fatalf("never co-listened artist is in a mix that already has enough tracks: %v", byArtist)
	}
}

func TestMixCapsOneArtistAndHasNoDuplicates(t *testing.T) {
	var l library
	seed, other := l.artist("A seed"), l.artist("B other") // equal libraries tie-break by name, so A seeds the first mix
	for i := 0; i < 40; i++ {
		l.track(seed, fmt.Sprint("s", i), "album")
	}
	for i := 0; i < 40; i++ {
		l.track(other, fmt.Sprint("o", i), "album") // shares an album, so it is "similar"
	}

	mix := l.home(nil).Mixes[0]
	if len(mix.TrackIDs) != mixSize {
		t.Fatalf("mix size = %d, want %d", len(mix.TrackIDs), mixSize)
	}
	seen := map[string]bool{}
	perArtist := map[uuid.UUID]int{}
	for _, id := range mix.TrackIDs {
		if seen[id] {
			t.Fatalf("duplicate track %s", id)
		}
		seen[id] = true
		for _, track := range l.tracks {
			if track.ID.String() == id {
				perArtist[track.ArtistID]++
			}
		}
	}
	if perArtist[seed] > int(seedShare*mixSize) || perArtist[other] > int(otherShare*mixSize) {
		t.Fatalf("cap exceeded: %v", perArtist)
	}
}

func TestMixChangesDailyButIsStableWithinADay(t *testing.T) {
	var l library
	seed := l.artist("Seed")
	for i := 0; i < 60; i++ {
		l.track(seed, fmt.Sprint("s", i), "")
	}

	today := ComputeHome(l.artists, l.tracks, nil, nil, statsNow).Mixes[0].TrackIDs
	later := ComputeHome(l.artists, l.tracks, nil, nil, statsNow.Add(3*time.Hour)).Mixes[0].TrackIDs
	tomorrow := ComputeHome(l.artists, l.tracks, nil, nil, statsNow.Add(24*time.Hour)).Mixes[0].TrackIDs

	if !slices.Equal(today, later) {
		t.Fatal("mix moved within one day")
	}
	if slices.Equal(today, tomorrow) {
		t.Fatal("mix did not change the next day")
	}
}

func TestColdStartStillBuildsHome(t *testing.T) {
	var l library
	big, small := l.artist("Big"), l.artist("Small")
	for i := 0; i < 5; i++ {
		l.track(big, fmt.Sprint("b", i), "")
	}
	l.track(small, "s", "")

	home := l.home(nil)
	if len(home.Artists) != 2 || home.Artists[0].ID != big.String() {
		t.Fatalf("cold start artists = %v", ids(home.Artists))
	}
	if len(home.Mixes) == 0 {
		t.Fatal("cold start has no mixes")
	}
	if len(home.OnRepeat)+len(home.Recent)+len(home.Rediscover) != 0 {
		t.Fatal("stat shelves should be empty without plays")
	}
}

func TestRecentOnRepeatAndRediscover(t *testing.T) {
	var l library
	a := l.artist("A")
	repeat, once, forgotten, skipped := l.track(a, "repeat", ""), l.track(a, "once", ""), l.track(a, "forgotten", ""), l.track(a, "skipped", "")

	plays := []Play{
		playAgo(forgotten, 100*24*time.Hour, 200),
		playAgo(forgotten, 101*24*time.Hour, 200),
		playAgo(repeat, 2*24*time.Hour, 200),
		playAgo(repeat, 3*24*time.Hour, 200),
		playAgo(repeat, 4*24*time.Hour, 200),
		playAgo(once, time.Hour, 200),
		playAgo(skipped, 10*time.Minute, 4),
	}

	home := l.home(plays)
	if !slices.Equal(home.OnRepeat, []string{repeat}) {
		t.Fatalf("on repeat = %v, want only %s", home.OnRepeat, repeat)
	}
	if !slices.Equal(home.Rediscover, []string{forgotten}) {
		t.Fatalf("rediscover = %v, want only %s", home.Rediscover, forgotten)
	}
	if len(home.Recent) == 0 || home.Recent[0] != once || slices.Contains(home.Recent, skipped) {
		t.Fatalf("recent = %v, want %s first and no skipped track", home.Recent, once)
	}
}

func TestRecordPlay(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	schema, err := migrations.FS.ReadFile("003_plays.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateArtist(ctx, "Artist"); err != nil {
		t.Fatal(err)
	}
	var artistID, trackID string
	if err := s.DB.QueryRow(ctx, `SELECT id FROM artists`).Scan(&artistID); err != nil {
		t.Fatal(err)
	}
	trackID = uuid.NewString()
	if _, err := s.DB.Exec(ctx, `INSERT INTO tracks (id, artist_id, title, duration, object_path) VALUES (?1, ?2, 't', 200, 'x')`,
		trackID, artistID); err != nil {
		t.Fatal(err)
	}

	play := uuid.NewString()
	if err := s.RecordPlay(ctx, play, trackID, 30, statsNow); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPlay(ctx, play, trackID, 90, statsNow); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPlay(ctx, play, trackID, 10, statsNow); err != nil { // stale checkpoint
		t.Fatal(err)
	}
	var seconds int
	if err := s.DB.QueryRow(ctx, `SELECT seconds FROM plays WHERE id = ?1`, play).Scan(&seconds); err != nil || seconds != 90 {
		t.Fatalf("seconds = %d, %v; want 90", seconds, err)
	}

	if err := s.RecordPlay(ctx, uuid.NewString(), uuid.NewString(), 30, statsNow); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown track error = %v, want ErrInvalid", err)
	}
	if err := s.RecordPlay(ctx, uuid.NewString(), trackID, 0, statsNow); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero seconds error = %v, want ErrInvalid", err)
	}

	// Plays go away with their track.
	if _, err := s.DB.Exec(ctx, `DELETE FROM tracks WHERE id = ?1`, trackID); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM plays`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("plays left = %d, %v", left, err)
	}
}
