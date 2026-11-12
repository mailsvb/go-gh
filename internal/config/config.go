// Package config stores the small amount of non-secret, non-event state the
// app needs across restarts. Events are never stored; secrets live in the OS
// keychain (see package secrets).
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Config is persisted as JSON in <UserConfigDir>/go-gh/config.json.
type Config struct {
	// InstallID identifies this installation. Webhooks created by the app
	// point to https://<ngrok-host>/hook/<InstallID>, which is how the app
	// finds its own hooks again after the ngrok URL changed.
	InstallID string `json:"installId"`
	// HookPersonalRepos enables per-repo webhooks for repositories that are
	// not covered by an organization webhook.
	HookPersonalRepos bool `json:"hookPersonalRepos"`
	// DisabledOrgs lists organizations the user excluded from org webhooks.
	DisabledOrgs []string `json:"disabledOrgs,omitempty"`
}

// Manager loads and saves Config.
type Manager struct {
	mu   sync.Mutex
	path string
	cfg  Config
}

// Dir returns the app's config directory.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "go-gh"), nil
}

// Load reads the config (creating defaults and an InstallID on first run).
func Load() (*Manager, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	return LoadFrom(filepath.Join(dir, "config.json"))
}

// LoadFrom reads the config from path.
func LoadFrom(path string) (*Manager, error) {
	m := &Manager{path: path, cfg: Config{HookPersonalRepos: true}}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &m.cfg); err != nil {
			return nil, err
		}
	}
	if m.cfg.InstallID == "" {
		m.cfg.InstallID = newID()
		if err := m.saveLocked(); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// Get returns a copy of the current config.
func (m *Manager) Get() Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.cfg
	c.DisabledOrgs = append([]string(nil), m.cfg.DisabledOrgs...)
	return c
}

// Update applies fn to the config and saves it.
func (m *Manager) Update(fn func(*Config)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(&m.cfg)
	return m.saveLocked()
}

func (m *Manager) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
