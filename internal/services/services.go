// Package services exposes the backend to the frontend via Wails bindings.
// Each exported method becomes a typed TypeScript function. The services are
// grouped by feature and stay thin wrappers around core.Core.
package services

import (
	"go-gh/internal/auth"
	"go-gh/internal/core"
)

// AppService gives the UI its initial data and triggers reloads. Later
// changes arrive as events (see the core.Evt* constants).
type AppService struct{ Core *core.Core }

// Snapshot returns the state, repositories and event history in one call.
func (s *AppService) Snapshot() core.Snapshot { return s.Core.Snapshot() }

// Refresh reloads organizations, repositories and the event history.
func (s *AppService) Refresh() { s.Core.Refresh() }

// AuthService handles the GitHub session.
type AuthService struct{ Core *core.Core }

// Login starts the GitHub device flow and returns the code to enter.
func (s *AuthService) Login() (auth.DeviceCode, error) { return s.Core.StartLogin() }

// CancelLogin aborts a pending login.
func (s *AuthService) CancelLogin() { s.Core.CancelLogin() }

// Logout removes webhooks, the tunnel and the stored token.
func (s *AuthService) Logout() { s.Core.Logout() }

// Retry tries to activate the stored session again.
func (s *AuthService) Retry() { s.Core.Retry() }

// NgrokService manages the ngrok authtoken. It works before login too; the
// tunnel connects once a session is active.
type NgrokService struct{ Core *core.Core }

// SetToken saves the ngrok authtoken in the keychain and connects.
func (s *NgrokService) SetToken(token string) error { return s.Core.SetNgrokToken(token) }

// ClearToken removes the ngrok authtoken and stops receiving webhooks.
func (s *NgrokService) ClearToken() error { return s.Core.ClearNgrokToken() }

// WebhookService chooses where webhooks are created.
type WebhookService struct{ Core *core.Core }

// SetOrgEnabled enables or disables the organization webhook for org.
func (s *WebhookService) SetOrgEnabled(org string, enabled bool) error {
	return s.Core.SetOrgEnabled(org, enabled)
}

// SetPersonalRepos enables or disables per-repository webhooks.
func (s *WebhookService) SetPersonalRepos(on bool) error {
	return s.Core.SetHookPersonalRepos(on)
}

// NotifyService manages OS notifications.
type NotifyService struct{ Core *core.Core }

// Check re-reads the OS notification permission.
func (s *NotifyService) Check() core.NotificationState { return s.Core.CheckNotifications() }

// Enable asks the OS for permission (a prompt appears only if the user was
// never asked) and returns the resulting state.
func (s *NotifyService) Enable() core.NotificationState { return s.Core.EnableNotifications() }

// Test shows a sample notification.
func (s *NotifyService) Test() { s.Core.TestNotification() }
