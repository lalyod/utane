package media

import (
	"io"
	"strings"
	"testing"
)

func TestStore(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.Put("tracks/a.mp3", strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	file, err := store.Open("tracks/a.mp3")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(file)
	file.Close()
	if string(got) != "data" {
		t.Fatalf("read %q", got)
	}

	if err := store.Remove("tracks/a.mp3"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open("tracks/a.mp3"); err == nil {
		t.Fatal("file still opens after Remove")
	}

	if err := store.Put("../escape.mp3", strings.NewReader("x")); err == nil {
		t.Fatal("Put accepted a path outside the store")
	}
	if got := ContentType("a/B.FLAC"); got != "audio/flac" {
		t.Fatalf("ContentType = %q", got)
	}
}
