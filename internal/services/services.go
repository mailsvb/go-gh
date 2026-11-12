// Package services exposes the backend to the frontend via Wails bindings.
// Each exported method becomes a typed TypeScript function.
package services

import (
	"go-gh/internal/auth"
	"go-gh/internal/core"
	"go-gh/internal/events"
	"go-gh/internal/gh"
)

// AuthService handles login state.
type AuthService struct{ Core *core.Core }

// State returns the current app state.
func (s *AuthService) State() core.State { return s.Core.State() }

// StartLogin starts the GitHub device flow and returns the code to enter.
func (s *AuthService) StartLogin() (auth.DeviceCode, error) { return s.Core.StartLogin() }

// CancelLogin aborts a pending login.
func (s *AuthService) CancelLogin() { s.Core.CancelLogin() }

// Logout removes webhooks, the tunnel and the stored token.
func (s *AuthService) Logout() { s.Core.Logout() }

// Retry tries to activate the stored session again.
func (s *AuthService) Retry() { s.Core.Retry() }

// RepoService lists repositories.
type RepoService struct{ Core *core.Core }

// Repos returns the repositories of the logged-in user.
func (s *RepoService) Repos() []gh.Repo { return s.Core.Repos() }

// Refresh reloads organizations, repositories and the event history.
func (s *RepoService) Refresh() { s.Core.Refresh() }

// EventService exposes the in-memory event history.
type EventService struct{ Core *core.Core }

// Recent returns up to 100 events, newest first.
func (s *EventService) Recent() []events.Event { return s.Core.Events() }

// SettingsService changes ngrok and webhook settings.
type SettingsService struct{ Core *core.Core }

// SetNgrokToken saves the ngrok authtoken in the keychain and connects.
func (s *SettingsService) SetNgrokToken(token string) error { return s.Core.SetNgrokToken(token) }

// ClearNgrokToken removes the ngrok authtoken and stops receiving webhooks.
func (s *SettingsService) ClearNgrokToken() error { return s.Core.ClearNgrokToken() }

// SetOrgEnabled enables or disables the organization webhook for org.
func (s *SettingsService) SetOrgEnabled(org string, enabled bool) error {
	return s.Core.SetOrgEnabled(org, enabled)
}

// SetHookPersonalRepos enables or disables per-repository webhooks.
func (s *SettingsService) SetHookPersonalRepos(on bool) error {
	return s.Core.SetHookPersonalRepos(on)
}
