package music

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testReleaseID = "123e4567-e89b-12d3-a456-426614174000"

func TestSearchAlbumCovers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			if r.URL.Path == "/covers/release/"+testReleaseID+"/front-250" {
				w.WriteHeader(http.StatusTemporaryRedirect)
				return
			}
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/ws/2/release/" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("fmt") != "json" || r.URL.Query().Get("limit") != "8" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		wantQuery := `release:"Album & More" AND artist:"Artist Name" AND date:"2024"`
		if r.URL.Query().Get("query") != wantQuery {
			t.Errorf("query = %q, want %q", r.URL.Query().Get("query"), wantQuery)
		}
		if r.UserAgent() != userAgent {
			t.Errorf("User-Agent = %q", r.UserAgent())
		}

		io.WriteString(w, `{"releases":[`+
			`{"id":"`+testReleaseID+`","title":"With Cover","date":"2024-03-01",`+
			`"artist-credit":[{"name":"One","joinphrase":" & "},{"name":"Two"}]},`+
			`{"id":"223e4567-e89b-12d3-a456-426614174000","title":"No Cover"}]}`)
	}))
	defer server.Close()

	candidates, err := searchCovers(
		context.Background(),
		server.Client(),
		server.URL+"/ws/2/release/",
		server.URL+"/covers",
		"Album & More",
		"2024",
		"Artist Name",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %#v", candidates)
	}
	if candidates[0].Artist != "One & Two" || candidates[0].Year != 2024 {
		t.Errorf("candidate = %#v", candidates[0])
	}
	wantThumbnail := server.URL + "/covers/release/" + testReleaseID + "/front-250"
	if candidates[0].Thumbnail != wantThumbnail {
		t.Errorf("thumbnail = %q, want %q", candidates[0].Thumbnail, wantThumbnail)
	}
}

func TestDownloadAlbumCoverRejectsBadResponses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		length string
		want   error
	}{
		{name: "upstream status", status: http.StatusNotFound},
		{name: "HTML", status: http.StatusOK, body: "<!doctype html><title>not an image</title>", want: ErrInvalidCover},
		{name: "SVG", status: http.StatusOK, body: `<svg xmlns="http://www.w3.org/2000/svg"/>`, want: ErrInvalidCover},
		{name: "oversized", status: http.StatusOK, length: "15728641", want: ErrCoverTooLarge},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/release/"+testReleaseID+"/front" {
					t.Errorf("path = %q", r.URL.Path)
				}
				if test.length != "" {
					w.Header().Set("Content-Length", test.length)
				}
				w.WriteHeader(test.status)
				io.WriteString(w, test.body)
			}))
			defer server.Close()

			_, _, _, err := downloadCover(context.Background(), server.Client(), server.URL, testReleaseID)
			if test.want == nil {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestDownloadAlbumCoverUsesRasterBytes(t *testing.T) {
	var imageData bytes.Buffer
	pixel := image.NewRGBA(image.Rect(0, 0, 1, 1))
	pixel.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&imageData, pixel); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write(imageData.Bytes())
	}))
	defer server.Close()

	data, contentType, extension, err := downloadCover(context.Background(), server.Client(), server.URL, testReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, imageData.Bytes()) || contentType != "image/png" || extension != ".png" {
		t.Fatalf("data/type/extension = %q, %q, %q", data, contentType, extension)
	}
}

func TestRasterImageTypeRejectsTruncatedPixels(t *testing.T) {
	var imageData bytes.Buffer
	pixel := image.NewRGBA(image.Rect(0, 0, 20, 20))
	if err := png.Encode(&imageData, pixel); err != nil {
		t.Fatal(err)
	}
	data := imageData.Bytes()
	if _, _, ok := rasterImageType(data[:len(data)-8]); ok {
		t.Error("truncated PNG should be rejected")
	}
}

func TestDownloadAlbumCoverRejectsUntrustedRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("untrusted redirect was followed")
	}))
	defer target.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	_, _, _, err := downloadCover(context.Background(), server.Client(), server.URL, testReleaseID)
	if err == nil || !strings.Contains(err.Error(), "untrusted host") {
		t.Fatalf("error = %v", err)
	}
}

func TestCoverRoutesValidateUUIDs(t *testing.T) {
	handler := &Handler{}

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/albums/not-a-uuid/cover/search", nil),
		httptest.NewRequest(http.MethodPost, "/albums/"+testReleaseID+"/cover/fetch", strings.NewReader(`{"release_id":"bad"}`)),
		httptest.NewRequest(http.MethodGet, "/tracks/not-a-uuid/cover", nil),
		httptest.NewRequest(http.MethodGet, "/tracks/not-a-uuid/cover/search", nil),
		httptest.NewRequest(http.MethodPost, "/tracks/not-a-uuid/cover/fetch", strings.NewReader(`{"release_id":"`+testReleaseID+`"}`)),
		httptest.NewRequest(http.MethodPost, "/tracks/"+testReleaseID+"/cover/fetch", strings.NewReader(`{"release_id":"bad"}`)),
		httptest.NewRequest(http.MethodDelete, "/tracks/not-a-uuid/cover", nil),
	} {
		response := httptest.NewRecorder()
		handler.Routes().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s %s: status = %d", request.Method, request.URL, response.Code)
		}
	}
}

func TestSearchCoversWithoutYear(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}

		wantQuery := `release:"Track Title" AND artist:"Artist Name"`
		if query := r.URL.Query().Get("query"); query != wantQuery {
			t.Errorf("query = %q, want %q", query, wantQuery)
		}
		if strings.Contains(r.URL.Query().Get("query"), "date:") {
			t.Error("track cover query must not include a year")
		}

		io.WriteString(w, `{"releases":[{"id":"`+testReleaseID+`","title":"Track Title"}]}`)
	}))
	defer server.Close()

	_, err := searchCovers(
		context.Background(),
		server.Client(),
		server.URL+"/ws/2/release/",
		server.URL+"/covers",
		"Track Title",
		"",
		"Artist Name",
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestTrackCoverEligibility(t *testing.T) {
	tests := []struct {
		name      string
		hasAlbum  bool
		hasCover  bool
		wantError bool
	}{
		{name: "standalone without cover"},
		{name: "standalone with cover", hasCover: true},
		{name: "album override", hasAlbum: true, hasCover: true},
		{name: "inherited album cover", hasAlbum: true, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := trackCoverEligibility(test.hasAlbum, test.hasCover)
			if test.wantError != errors.Is(err, ErrConflict) {
				t.Fatalf("error = %v, want conflict = %t", err, test.wantError)
			}
		})
	}
}
