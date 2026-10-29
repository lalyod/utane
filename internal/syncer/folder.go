package syncer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// A synced folder (Google Drive for desktop, OneDrive, Syncthing, a USB stick...) holds one
// <deviceId>.json per device plus a shared media/ directory. Each device writes only its own json,
// so the folder tool never has to resolve a conflict.

type folderSource struct {
	dir  string // the shared folder
	file string // peer snapshot file name
}

func (f *folderSource) Snapshot(context.Context) (*Snapshot, error) {
	data, err := os.ReadFile(filepath.Join(f.dir, f.file))
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{}
	if err := json.Unmarshal(data, snap); err != nil {
		return nil, err
	}

	return snap, nil
}

func (f *folderSource) Fetch(_ context.Context, name string) (io.ReadCloser, error) {
	root, err := os.OpenRoot(filepath.Join(f.dir, "media"))
	if err != nil {
		return nil, err
	}
	defer root.Close()

	return root.Open(filepath.FromSlash(name))
}

// pullFolder merges every other device's snapshot found in dir.
func (s *Syncer) pullFolder(ctx context.Context, dir string, self string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	changed := 0
	var problems []error
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") || name == self+".json" {
			continue
		}
		n, err := s.merger.Merge(ctx, &folderSource{dir: dir, file: name})
		changed += n
		if err != nil {
			problems = append(problems, errors.New(name+": "+err.Error()))
		}
	}

	return changed, errors.Join(problems...)
}

// pushFolder copies media the folder lacks, then writes this device's snapshot. The json goes last
// so a peer never sees a row whose file has not been uploaded yet.
func (s *Syncer) pushFolder(ctx context.Context, dir string, self string) error {
	snap, err := Export(ctx, s.DB, self)
	if err != nil {
		return err
	}

	mediaDir := filepath.Join(dir, "media")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(mediaDir)
	if err != nil {
		return err
	}
	defer root.Close()

	// shortcut: files of deleted tracks stay in the shared folder's media/; nothing sweeps them.
	for _, name := range snap.MediaNames() {
		local := filepath.FromSlash(name)
		if _, err := root.Stat(local); err == nil {
			continue
		}
		src, err := s.Media.Open(name)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // row points at a file this device lost; nothing to share
			}

			return err
		}
		err = copyAtomic(root, local, src)
		src.Close()
		if err != nil {
			return err
		}
	}

	data, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	target := filepath.Join(dir, self+".json")
	if existing, err := os.ReadFile(target); err == nil && bytes.Equal(existing, data) {
		return nil
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}

	return os.Rename(tmp, target)
}

// copyAtomic writes through a .part file so a sync tool never uploads half a track under its
// final name.
func copyAtomic(root *os.Root, name string, src io.Reader) error {
	if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	part := name + ".part"
	dst, err := root.Create(part)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		root.Remove(part)

		return err
	}
	if err := dst.Close(); err != nil {
		root.Remove(part)

		return err
	}

	return root.Rename(part, name)
}
