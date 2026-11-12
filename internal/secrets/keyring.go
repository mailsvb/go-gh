// Package secrets stores credentials in the OS keychain (macOS Keychain,
// Windows Credential Manager) via go-keyring.
package secrets

import (
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/zalando/go-keyring"
)

const service = "go-gh"

// Keys used in the keychain.
const (
	GitHubToken   = "github-token"
	NgrokToken    = "ngrok-authtoken"
	WebhookSecret = "webhook-secret"
)

// Store is the keychain abstraction (swappable in tests).
type Store interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// Keyring is the OS keychain implementation of Store.
type Keyring struct{}

// Get returns the value for key, or "" if it is not set.
func (Keyring) Get(key string) (string, error) {
	v, err := keyring.Get(service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	return v, err
}

// Set stores value under key.
func (Keyring) Set(key, value string) error { return keyring.Set(service, key, value) }

// Delete removes key; a missing key is not an error.
func (Keyring) Delete(key string) error {
	err := keyring.Delete(service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// EnsureWebhookSecret returns the webhook HMAC secret, generating and
// storing a random one on first use.
func EnsureWebhookSecret(s Store) (string, error) {
	v, err := s.Get(WebhookSecret)
	if err != nil || v != "" {
		return v, err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	v = hex.EncodeToString(b)
	return v, s.Set(WebhookSecret, v)
}

// Memory is an in-memory Store for tests.
type Memory map[string]string

func (m Memory) Get(k string) (string, error) { return m[k], nil }
func (m Memory) Set(k, v string) error        { m[k] = v; return nil }
func (m Memory) Delete(k string) error        { delete(m, k); return nil }
