package music

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLookupLyrics(t *testing.T) {
	tests := []struct {
		name         string
		exactStatus  int
		exact        string
		search       string
		want         string
		wantErr      error
		wantSearches int
	}{
		{
			name:  "Japanese native candidate beats Latin",
			exact: `{"syncedLyrics":"[00:01.00]Kimi no koe"}`,
			search: `[
				{"trackName":"A Track","artistName":"Other Artist","albumName":"An Album","duration":123,"syncedLyrics":"[00:01.00]きみの声"},
				{"trackName":"A Track","artistName":"An Artist","albumName":"An Album","duration":123,"syncedLyrics":"[00:01.00]Kimi no koe"},
				{"trackName":"A Track","artistName":"An Artist","albumName":"An Album","duration":124.2,"syncedLyrics":"[00:01.00]きみの声"}
			]`,
			want:         "[00:01.00]きみの声",
			wantSearches: 1,
		},
		{
			name:  "Korean native candidate beats Latin",
			exact: `{"syncedLyrics":"[00:01.00]Saranghae"}`,
			search: `[
				{"trackName":"A Track","artistName":"An Artist","albumName":"An Album","duration":123,"syncedLyrics":"[00:01.00]Saranghae"},
				{"trackName":"A Track","artistName":"An Artist","albumName":"An Album","duration":121,"syncedLyrics":"[00:01.00]사랑해"}
			]`,
			want:         "[00:01.00]사랑해",
			wantSearches: 1,
		},
		{
			name:  "unrelated result keeps exact fallback",
			exact: `{"syncedLyrics":"[00:01.00]Original Latin"}`,
			search: `[
				{"trackName":"Another Track","artistName":"An Artist","albumName":"An Album","duration":123,"syncedLyrics":"[00:01.00]ちがう歌"},
				{"trackName":"A Track","artistName":"An Artist","albumName":"An Album","duration":123,"syncedLyrics":"[00:01.00]Search Latin"}
			]`,
			want:         "[00:01.00]Original Latin",
			wantSearches: 1,
		},
		{
			name:         "native exact avoids search",
			exact:        `{"syncedLyrics":"[ar:Romanized]\n[00:01.00]そのまま"}`,
			want:         "[ar:Romanized]\n[00:01.00]そのまま",
			wantSearches: 0,
		},
		{
			name:  "native metadata does not hide Latin lyrics",
			exact: `{"syncedLyrics":"[ar:宇多田ヒカル]\n[00:01.00]Latin lyrics"}`,
			search: `[
				{"trackName":"A Track","artistName":"An Artist","albumName":"An Album","duration":123,"syncedLyrics":"[00:01.00]日本語の歌詞"}
			]`,
			want:         "[00:01.00]日本語の歌詞",
			wantSearches: 1,
		},
		{
			name:         "404 exact uses safe search result",
			exactStatus:  http.StatusNotFound,
			search:       `[{"trackName":"A Track","artistName":"An Artist","albumName":"An Album","duration":125.9,"syncedLyrics":"[00:01.00]Found"}]`,
			want:         "[00:01.00]Found",
			wantSearches: 1,
		},
		{
			name:         "404 exact rejects distant duration",
			exactStatus:  http.StatusNotFound,
			search:       `[{"trackName":"A Track","artistName":"An Artist","albumName":"An Album","duration":130,"syncedLyrics":"[00:01.00]Found"}]`,
			wantErr:      ErrLyricsNotFound,
			wantSearches: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			searches := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("User-Agent"); got != "HokanNoUta/1.0" {
					t.Errorf("User-Agent = %q", got)
				}
				if got := r.URL.Query().Get("token"); got != "configured" {
					t.Errorf("token = %q", got)
				}

				switch r.URL.Path {
				case "/custom/api/get":
					if got := r.URL.Query().Get("duration"); got != "123" {
						t.Errorf("duration = %q", got)
					}
					if test.exactStatus != 0 {
						w.WriteHeader(test.exactStatus)
						return
					}
					_, _ = fmt.Fprint(w, test.exact)
				case "/custom/api/search":
					searches++
					if r.URL.Query().Has("duration") {
						t.Error("search unexpectedly included duration")
					}
					for key, want := range map[string]string{
						"artist_name": "An Artist",
						"album_name":  "An Album",
						"track_name":  "A Track",
					} {
						if got := r.URL.Query().Get(key); got != want {
							t.Errorf("%s = %q", key, got)
						}
					}
					_, _ = fmt.Fprint(w, test.search)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			lyrics, err := lookupLyrics(
				context.Background(),
				server.Client(),
				server.URL+"/custom/api/get?token=configured",
				"An Artist",
				"An Album",
				"A Track",
				123,
			)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			if lyrics != test.want {
				t.Fatalf("lyrics = %q, want %q", lyrics, test.want)
			}
			if searches != test.wantSearches {
				t.Fatalf("search requests = %d, want %d", searches, test.wantSearches)
			}
		})
	}
}
