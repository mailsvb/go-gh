package main

import (
	"embed"
	"fmt"
	"log"
	"log/slog"
	"os"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/icons"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"go-gh/internal/config"
	"go-gh/internal/core"
	appevents "go-gh/internal/events"
	"go-gh/internal/gh"
	"go-gh/internal/secrets"
	"go-gh/internal/services"
)

//go:embed all:frontend/dist
var assets embed.FS

// Set at build time:
//
//	-ldflags "-X main.githubClientID=<GitHub App client ID> -X main.githubAppSlug=<app URL name> -X main.version=<version>"
var (
	githubClientID = ""
	githubAppSlug  = ""
	version        = "dev"
)

func init() {
	// Typed events for the generated TypeScript bindings.
	application.RegisterEvent[core.State](core.EvtState)
	application.RegisterEvent[appevents.Event](core.EvtNewEvent)
	application.RegisterEvent[[]appevents.Event](core.EvtEventsSet)
	application.RegisterEvent[[]gh.Repo](core.EvtRepos)
}

func main() {
	clientID := githubClientID
	if v := os.Getenv("GOGH_GITHUB_CLIENT_ID"); v != "" {
		clientID = v
	}
	appSlug := githubAppSlug
	if v := os.Getenv("GOGH_GITHUB_APP_SLUG"); v != "" {
		appSlug = v
	}
	gh.UserAgent = "go-gh/" + version

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	ns := notifications.New()
	var app *application.App
	c := core.New(core.Options{
		ClientID: clientID,
		AppSlug:  appSlug,
		Secrets:  secrets.Keyring{},
		Config:   cfg,
		Notifier: notifier{ns: ns},
		Emit: func(name string, data any) {
			if app != nil {
				app.Event.Emit(name, data)
			}
		},
		Log: slog.Default(),
	})

	app = application.New(application.Options{
		Name:        "go-gh",
		Description: "GitHub events in your tray",
		Services: []application.Service{
			application.NewService(ns),
			application.NewService(&services.AuthService{Core: c}),
			application.NewService(&services.RepoService{Core: c}),
			application.NewService(&services.EventService{Core: c}),
			application.NewService(&services.SettingsService{Core: c}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			// Menu-bar app: no Dock icon, no app switcher entry.
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
		OnShutdown: c.Shutdown,
	})

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "go-gh",
		Width:            420,
		Height:           640,
		Frameless:        true,
		AlwaysOnTop:      true,
		Hidden:           true,
		DisableResize:    true,
		HideOnEscape:     true,
		HideOnFocusLost:  true,
		BackgroundColour: application.NewRGB(13, 17, 23),
		URL:              "/",
		Windows: application.WindowsWindow{
			HiddenOnTaskbar: true,
		},
	})
	// Closing the popup only hides it; the app keeps running in the tray.
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		window.Hide()
		e.Cancel()
	})

	tray := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(icons.SystrayMacTemplate)
	} else {
		tray.SetIcon(icons.SystrayLight)
		tray.SetDarkModeIcon(icons.SystrayDark)
	}
	tray.SetTooltip("go-gh")

	menu := application.NewMenu()
	menu.Add("Open go-gh").OnClick(func(*application.Context) { tray.ShowWindow() })
	menu.Add("Refresh").OnClick(func(*application.Context) { c.Refresh() })
	menu.AddSeparator()
	status := menu.Add("Starting…").SetEnabled(false)
	menu.AddSeparator()
	menu.Add("Quit go-gh").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu)
	tray.AttachWindow(window).WindowOffset(5)

	// Keep the tray status line in sync with the backend state.
	app.Event.On(core.EvtState, func(e *application.CustomEvent) {
		if s, ok := e.Data.(core.State); ok {
			label := statusLabel(s)
			status.SetLabel(label)
			menu.Update()
			tray.SetTooltip("go-gh — " + label)
		}
	})

	// Clicking a notification opens the event on github.com.
	ns.OnNotificationResponse(func(r notifications.NotificationResult) {
		if r.Error != nil {
			return
		}
		if u, ok := r.Response.UserInfo["url"].(string); ok && u != "" {
			_ = app.Browser.OpenURL(u)
		}
	})

	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		if ok, err := ns.RequestNotificationAuthorization(); err != nil || !ok {
			slog.Warn("notifications not authorized", "err", err)
		}
		c.Start()
	})
	app.Event.OnApplicationEvent(events.Common.SystemDidWake, func(*application.ApplicationEvent) {
		c.OnWake()
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

func statusLabel(s core.State) string {
	switch s.Phase {
	case core.PhaseStarting:
		return "Starting…"
	case core.PhaseLoggedOut:
		return "Not logged in"
	case core.PhaseLoggingIn:
		return "Waiting for GitHub login…"
	}
	user := ""
	if s.User != nil {
		user = s.User.Login + " · "
	}
	switch s.Tunnel.Status {
	case "online":
		return fmt.Sprintf("%swebhooks: %d ok, %d failed", user, s.Hooks.OK, s.Hooks.Failed)
	case "connecting":
		return user + "connecting to ngrok…"
	case "error":
		return user + "ngrok error"
	}
	return user + "webhooks off (set ngrok token)"
}

// notifier sends OS notifications through the Wails notifications service.
type notifier struct {
	ns *notifications.NotificationService
}

func (n notifier) Notify(e appevents.Event) {
	body := e.Title
	if e.Detail != "" {
		body += "\n" + e.Detail
	}
	err := n.ns.SendNotification(notifications.NotificationOptions{
		ID:       e.Key,
		Title:    e.Short(),
		Body:     body,
		ThreadID: e.Repo,
		Data:     map[string]any{"url": e.URL},
	})
	if err != nil {
		slog.Warn("sending notification", "err", err)
	}
}
