package media

import (
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Store keeps media files under one directory. os.Root rejects names that escape it.
type Store struct {
	root *os.Root
}

func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}

	return &Store{root: root}, nil
}

func (s *Store) Close() error { return s.root.Close() }

func (s *Store) Put(name string, r io.Reader) error {
	name = filepath.FromSlash(name)
	if err := s.root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	file, err := s.root.Create(name)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, r); err != nil {
		file.Close()
		s.root.Remove(name)

		return err
	}

	return file.Close()
}

func (s *Store) Open(name string) (*os.File, error) {
	return s.root.Open(filepath.FromSlash(name))
}

func (s *Store) Remove(name string) error {
	return s.root.Remove(filepath.FromSlash(name))
}

// shortcut: fixed extension table instead of stored content types; add a row when a format is unplayable.
var contentTypes = map[string]string{
	".mp3":  "audio/mpeg",
	".m4a":  "audio/mp4",
	".aac":  "audio/aac",
	".flac": "audio/flac",
	".ogg":  "audio/ogg",
	".oga":  "audio/ogg",
	".opus": "audio/ogg",
	".wav":  "audio/wav",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".avif": "image/avif",
}

// ContentType returns the media type for a stored name, or "" when unknown.
func ContentType(name string) string {
	return contentTypes[strings.ToLower(path.Ext(name))]
}
