package music

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"time"

	"github.com/lalyod/utane/internal/model"
)

// Everything the home page shows is computed here from the listening log.
//
//   - Track affinity: each play adds its completion (listened / duration) weighted by an exponential
//     decay with a 30 day half-life; a skip (completion < 0.2) subtracts a little instead.
//   - Artist affinity: the sum of 1-exp(-affinity) over the artist's tracks, so one looped song cannot
//     crown an artist.
//   - Artist similarity: item-based collaborative filtering. Two artists are close when they show up
//     in the same listening sessions (cosine over session membership), blended with the artists that
//     share playlists or albums so a thin log still gives useful mixes.
//   - Mixes: for each top artist, rank every track by seed match + similarity + affinity + novelty,
//     cap how much one artist can take, and order the pick with a per-day hash so it changes daily.

const (
	StatsWindow = 180 * 24 * time.Hour

	halfLifeDays   = 30.0
	sessionGap     = 30 * time.Minute
	skipCompletion = 0.2
	skipPenalty    = 0.25
	mixSize        = 25
	mixMinSize     = 10
	mixCount       = 4
	seedShare      = 0.6
	otherShare     = 0.4
	homeArtists    = 12
	homeTracks     = 12
	onRepeatDays   = 30
	rediscoverDays = 60
)

type Play struct {
	TrackID  string
	PlayedAt int64 // unix ms, start of the listen
	Seconds  int
}

type HomeArtist struct {
	ID     string  `json:"id"`
	Plays  int     `json:"plays"`
	Hours  float64 `json:"hours"`
	Tracks int     `json:"tracks"`
}

type Mix struct {
	Name         string   `json:"name"`
	SeedArtistID string   `json:"seed_artist_id"`
	TrackIDs     []string `json:"track_ids"`
}

type Home struct {
	Artists    []HomeArtist `json:"artists"`
	Mixes      []Mix        `json:"mixes"`
	OnRepeat   []string     `json:"on_repeat"`
	Recent     []string     `json:"recent"`
	Rediscover []string     `json:"rediscover"`
}

func (s *Service) Home(ctx context.Context, now time.Time) (*Home, error) {
	artists, err := s.GetArtists(ctx)
	if err != nil {
		return nil, err
	}
	tracks, err := s.GetTracks(ctx)
	if err != nil {
		return nil, err
	}
	playlists, err := s.GetPlaylists(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := s.DB.Query(ctx,
		`SELECT track_id, played_at, seconds FROM plays WHERE played_at >= ?1 ORDER BY played_at`,
		now.Add(-StatsWindow).UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var plays []Play
	for rows.Next() {
		var p Play
		if err := rows.Scan(&p.TrackID, &p.PlayedAt, &p.Seconds); err != nil {
			return nil, err
		}
		plays = append(plays, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return ComputeHome(artists, tracks, playlists, plays, now), nil
}

// RecordPlay stores or extends one listen. Re-sending a checkpoint with fewer seconds is a no-op.
func (s *Service) RecordPlay(ctx context.Context, id string, trackID string, seconds int, now time.Time) error {
	if seconds < 1 || seconds > 4*3600 {
		return ErrInvalid
	}
	_, err := s.DB.Exec(ctx, `
		INSERT INTO plays (id, track_id, played_at, seconds, updated_at) VALUES (?1, ?2, ?3, ?4, ?5)
		ON CONFLICT (id) DO UPDATE SET seconds = excluded.seconds, updated_at = excluded.updated_at
		WHERE excluded.seconds > plays.seconds AND plays.track_id = excluded.track_id`,
		id, trackID, now.Add(-time.Duration(seconds)*time.Second).UnixMilli(), seconds, now.UnixMilli())

	return serviceError(err)
}

type listen struct {
	trackID    string
	artistID   string
	at         int64
	seconds    int
	completion float64
	ageDays    float64
}

func completion(seconds int, duration int) float64 {
	if duration <= 0 {
		return 1
	}

	return math.Min(1, float64(seconds)/float64(duration))
}

// counted is a listen that says the track was actually heard, not skipped.
func (l listen) counted() bool { return l.seconds >= 30 || l.completion >= 0.5 }

// unit maps (day, id) to a stable value in [0,1).
func unit(day int64, salt string, id string) float64 {
	h := fnv.New32a()
	fmt.Fprintf(h, "%d|%s|%s", day, salt, id)

	return float64(h.Sum32()) / float64(math.MaxUint32)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	return keys
}

// cosine of two membership sets.
func cosine(a map[int]struct{}, b map[int]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	small, large := a, b
	if len(b) < len(a) {
		small, large = b, a
	}
	shared := 0
	for k := range small {
		if _, ok := large[k]; ok {
			shared++
		}
	}

	return float64(shared) / math.Sqrt(float64(len(a))*float64(len(b)))
}

func ComputeHome(artists []model.Artist, tracks []model.Track, playlists []model.Playlist, plays []Play, now time.Time) *Home {
	home := &Home{
		Artists:    []HomeArtist{},
		Mixes:      []Mix{},
		OnRepeat:   []string{},
		Recent:     []string{},
		Rediscover: []string{},
	}

	byTrack := map[string]model.Track{}
	trackCount := map[string]int{}
	for _, t := range tracks {
		byTrack[t.ID.String()] = t
		trackCount[t.ArtistID.String()]++
	}
	artistName := map[string]string{}
	for _, a := range artists {
		artistName[a.ID.String()] = a.Name
	}

	nowMs := now.UnixMilli()
	listens := make([]listen, 0, len(plays))
	for _, p := range plays {
		t, ok := byTrack[p.TrackID]
		if !ok || p.Seconds < 1 {
			continue
		}
		listens = append(listens, listen{
			trackID:    p.TrackID,
			artistID:   t.ArtistID.String(),
			at:         p.PlayedAt,
			seconds:    p.Seconds,
			completion: completion(p.Seconds, t.Duration),
			ageDays:    math.Max(0, float64(nowMs-p.PlayedAt)/float64(24*time.Hour/time.Millisecond)),
		})
	}
	sort.SliceStable(listens, func(i, j int) bool { return listens[i].at < listens[j].at })

	// Track and artist statistics.
	affinity := map[string]float64{}       // decayed, all time in window
	affinityRecent := map[string]float64{} // only the last onRepeatDays
	rawLove := map[string]float64{}        // undecayed completion, for rediscover
	countedPlays := map[string]int{}
	countedRecent := map[string]int{}
	countedOld := map[string]int{}
	lastPlayed := map[string]float64{} // ageDays of newest listen
	artistPlays := map[string]int{}
	artistSeconds := map[string]int{}
	for _, l := range listens {
		decay := math.Pow(0.5, l.ageDays/halfLifeDays)
		delta := l.completion * decay
		if l.completion < skipCompletion {
			delta = -skipPenalty * decay
		}
		affinity[l.trackID] += delta
		if l.ageDays <= onRepeatDays {
			affinityRecent[l.trackID] += delta
		}
		if age, ok := lastPlayed[l.trackID]; !ok || l.ageDays < age {
			lastPlayed[l.trackID] = l.ageDays
		}
		artistSeconds[l.artistID] += l.seconds
		if l.counted() {
			countedPlays[l.trackID]++
			artistPlays[l.artistID]++
			rawLove[l.trackID] += l.completion
			if l.ageDays <= onRepeatDays {
				countedRecent[l.trackID]++
			}
			if l.ageDays > rediscoverDays {
				countedOld[l.trackID]++
			}
		}
	}

	artistAffinity := map[string]float64{}
	maxTrack := 0.0
	for id, a := range affinity {
		a = math.Max(0, a)
		affinity[id] = a
		maxTrack = math.Max(maxTrack, a)
		artistAffinity[byTrack[id].ArtistID.String()] += 1 - math.Exp(-a)
	}

	// Seeds and the favorites shelf: affinity first, then library size, then name.
	ranked := make([]string, 0, len(artistName))
	for id := range artistName {
		ranked = append(ranked, id)
	}
	sort.Slice(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if artistAffinity[a] != artistAffinity[b] {
			return artistAffinity[a] > artistAffinity[b]
		}
		if trackCount[a] != trackCount[b] {
			return trackCount[a] > trackCount[b]
		}
		if artistName[a] != artistName[b] {
			return artistName[a] < artistName[b]
		}

		return a < b
	})
	// Artists you have not (really) listened to follow the ones you have, so the shelf never empties
	// just because the log is thin or only holds skips.
	for _, id := range ranked {
		if len(home.Artists) == homeArtists || trackCount[id] == 0 {
			break
		}
		home.Artists = append(home.Artists, HomeArtist{
			ID:     id,
			Plays:  artistPlays[id],
			Hours:  math.Round(float64(artistSeconds[id])/36) / 100,
			Tracks: trackCount[id],
		})
	}

	// Sessions: a new one starts after a gap of silence. Only real listens link artists.
	sessions := map[string]map[int]struct{}{}
	session := 0
	var lastEnd int64
	for _, l := range listens {
		if !l.counted() {
			continue
		}
		if lastEnd != 0 && l.at-lastEnd > sessionGap.Milliseconds() {
			session++
		}
		lastEnd = max(lastEnd, l.at+int64(l.seconds)*1000)
		if sessions[l.artistID] == nil {
			sessions[l.artistID] = map[int]struct{}{}
		}
		sessions[l.artistID][session] = struct{}{}
	}
	inPlaylists := map[string]map[int]struct{}{}
	for i, p := range playlists {
		for _, id := range p.TrackIDs {
			if t, ok := byTrack[id]; ok {
				artist := t.ArtistID.String()
				if inPlaylists[artist] == nil {
					inPlaylists[artist] = map[int]struct{}{}
				}
				inPlaylists[artist][i] = struct{}{}
			}
		}
	}
	onAlbum := map[string]map[string]struct{}{}
	for _, t := range tracks {
		if t.AlbumID == "" {
			continue
		}
		if onAlbum[t.AlbumID] == nil {
			onAlbum[t.AlbumID] = map[string]struct{}{}
		}
		onAlbum[t.AlbumID][t.ArtistID.String()] = struct{}{}
	}
	albumMates := map[string]map[string]struct{}{}
	for _, members := range onAlbum {
		for a := range members {
			for b := range members {
				if a == b {
					continue
				}
				if albumMates[a] == nil {
					albumMates[a] = map[string]struct{}{}
				}
				albumMates[a][b] = struct{}{}
			}
		}
	}
	similarity := func(a string, b string) float64 {
		sim := cosine(sessions[a], sessions[b])
		sim = math.Max(sim, 0.7*cosine(inPlaylists[a], inPlaylists[b]))
		if _, ok := albumMates[a][b]; ok {
			sim = math.Max(sim, 0.5)
		}

		return sim
	}

	day := now.UTC().Unix() / 86400
	newest := make([]model.Track, len(tracks))
	copy(newest, tracks)
	sort.SliceStable(newest, func(i, j int) bool { return newest[i].CreatedAt.After(newest[j].CreatedAt) })

	for _, seed := range ranked {
		if len(home.Mixes) == mixCount {
			break
		}
		if trackCount[seed] == 0 {
			continue
		}
		type candidate struct {
			id     string
			artist string
			score  float64
			linked bool
		}
		var pool []candidate
		for _, t := range tracks {
			id, artist := t.ID.String(), t.ArtistID.String()
			sim := 0.0
			if artist != seed {
				sim = similarity(artist, seed)
			}
			novelty := 1 / float64(1+countedPlays[id])
			score := 0.8*sim + 0.15*novelty + 0.05*unit(day, "score", id)
			if maxTrack > 0 {
				score += 0.3 * affinity[id] / maxTrack
			}
			if artist == seed {
				score += 1
			}
			pool = append(pool, candidate{id, artist, score, artist == seed || sim > 0})
		}
		linked := 0
		for _, c := range pool {
			if c.linked {
				linked++
			}
		}
		if linked >= mixMinSize { // unrelated tracks only top up a mix that would be too short
			kept := pool[:0]
			for _, c := range pool {
				if c.linked {
					kept = append(kept, c)
				}
			}
			pool = kept
		}
		sort.Slice(pool, func(i, j int) bool {
			if pool[i].linked != pool[j].linked {
				return pool[i].linked
			}
			if pool[i].score != pool[j].score {
				return pool[i].score > pool[j].score
			}

			return pool[i].id < pool[j].id
		})

		picked := map[string]bool{}
		perArtist := map[string]int{}
		var ids []string
		pass := func(strict bool) {
			for _, c := range pool {
				if len(ids) == mixSize {
					return
				}
				if picked[c.id] {
					continue
				}
				if strict {
					limit := int(otherShare * mixSize)
					if c.artist == seed {
						limit = int(seedShare * mixSize)
					}
					if perArtist[c.artist] >= limit {
						continue
					}
				}
				picked[c.id] = true
				perArtist[c.artist]++
				ids = append(ids, c.id)
			}
		}
		pass(true)
		pass(false)
		if len(ids) < 3 {
			continue
		}
		// Linked candidates first made the selection; the per-day hash decides the play order.
		sort.Slice(ids, func(i, j int) bool { return unit(day, "order", ids[i]) < unit(day, "order", ids[j]) })
		home.Mixes = append(home.Mixes, Mix{Name: artistName[seed] + " Mix", SeedArtistID: seed, TrackIDs: ids})
	}

	// On repeat: heavily played in the last month and more than once.
	for _, id := range sortedKeys(affinityRecent) {
		if countedRecent[id] >= 2 && affinityRecent[id] > 0 {
			home.OnRepeat = append(home.OnRepeat, id)
		}
	}
	sort.SliceStable(home.OnRepeat, func(i, j int) bool {
		return affinityRecent[home.OnRepeat[i]] > affinityRecent[home.OnRepeat[j]]
	})
	home.OnRepeat = home.OnRepeat[:min(len(home.OnRepeat), homeTracks)]

	// Jump back in: latest distinct tracks that were not skipped.
	seen := map[string]bool{}
	for i := len(listens) - 1; i >= 0 && len(home.Recent) < homeTracks; i-- {
		l := listens[i]
		if l.completion < skipCompletion || seen[l.trackID] {
			continue
		}
		seen[l.trackID] = true
		home.Recent = append(home.Recent, l.trackID)
	}

	// Rediscover: loved before, silent for two months.
	for _, id := range sortedKeys(rawLove) {
		if countedOld[id] >= 2 && lastPlayed[id] > rediscoverDays {
			home.Rediscover = append(home.Rediscover, id)
		}
	}
	sort.SliceStable(home.Rediscover, func(i, j int) bool {
		return rawLove[home.Rediscover[i]] > rawLove[home.Rediscover[j]]
	})
	home.Rediscover = home.Rediscover[:min(len(home.Rediscover), homeTracks)]

	return home
}
