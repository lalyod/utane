package syncer

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// Config is stored next to the database as sync.json. GroupCode is the shared secret LAN peers
// must agree on, so the file is owner-only.
type Config struct {
	DeviceID  string `json:"deviceId"`
	GroupCode string `json:"groupCode"`
	Folder    string `json:"folder"`
	LAN       bool   `json:"lan"`
}

// no I, O, 0, 1: the code is read off one screen and typed into another.
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func newGroupCode() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		buf[i] = codeAlphabet[int(b)%len(codeAlphabet)]
	}

	return string(buf), nil
}

func loadConfig(dir string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(filepath.Join(dir, "sync.json"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return cfg, err
	}
	if err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return cfg, err
		}
	}

	changed := false
	if cfg.DeviceID == "" {
		cfg.DeviceID = uuid.NewString()
		changed = true
	}
	if cfg.GroupCode == "" {
		if cfg.GroupCode, err = newGroupCode(); err != nil {
			return cfg, err
		}
		changed = true
	}
	if changed {
		return cfg, saveConfig(dir, cfg)
	}

	return cfg, nil
}

func saveConfig(dir string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, "sync.json"), data, 0o600)
}
