package main

import (
	"embed"
	"flag"
	"os"

	"github.com/CodeNeow/MyLlama/core"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed frontend/dist
var assets embed.FS

//go:embed build/windows/icon.ico
var trayIcon []byte

// mainWindowName is the unique name of the single application window; core
// looks the window up by this name (application.Get().Window.GetByName) for
// runtime background-colour switches from the tray / theme setters.
const mainWindowName = "main"

func main() {
	// Mode flags for API-route (headless) mode: --headless forces headless
	// (used by the GUI → headless relaunch), --gui forces the GUI even when
	// the persisted apiRouteMode preference requests headless (recovery
	// escape hatch). ContinueOnError keeps unknown arguments from third-party
	// launchers harmless.
	flags := flag.NewFlagSet("myllama", flag.ContinueOnError)
	headless := flags.Bool("headless", false, "run in headless API-route mode (tray + llama-server only, no GUI)")
	gui := flags.Bool("gui", false, "force GUI mode even when apiRouteMode is enabled in the config")
	_ = flags.Parse(os.Args[1:])

	app := core.NewApp()

	// Tray icon is injected before the GUI starts (needed because
	// SetTrayEnabled may start/stop the tray at runtime); main only declares
	// the embed, and the bytes are exposed to core via core.TrayIcon.
	core.TrayIcon = trayIcon

	// Headless branch: skip the Wails application entirely (no WebView2
	// process tree) — core.RunHeadless loads the config, takes the
	// single-instance mutex, starts the headless tray and starts/adopts
	// llama-server.
	if core.ShouldRunHeadless(*headless, *gui) {
		core.RunHeadless(trayIcon)
		return
	}

	// GUI mode takes the same single-instance mutex (fixes double-launch);
	// the mutex retry window also covers the headless → GUI handover.
	if !core.AcquireSingleInstance() {
		println("MyLlama is already running.")
		return
	}

	// Wails v3 application: the core.App service is bound to the frontend
	// (all exported methods except the v3 lifecycle hooks), and the embedded
	// frontend/dist is served through the asset handler. Without the
	// FRONTEND_DEVSERVER_URL env var (set only by `wails3 dev`) the embedded
	// assets are served directly.
	wailsApp := application.New(application.Options{
		Name:     "MyLlama",
		Services: []application.Service{application.NewService(app)},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
	})

	// Single frameless main window. Created before Run, so the window is
	// queued and actually built after App.ServiceStartup — meaning the
	// startup-time background-colour switch (saved theme applied via
	// SetBackgroundColour, which only mutates window options until the
	// window is realized) lands before first paint. Startup sequencing
	// (config load, handover adoption, monitor, tray) lives in
	// core.App.ServiceStartup, the v3 equivalent of the v2 OnStartup hook.
	wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             mainWindowName,
		Title:            "MyLlama",
		Width:            1200,
		Height:           800,
		MinWidth:         900,
		MinHeight:        600,
		Frameless:        true,
		StartState:       application.WindowStateNormal,
		BackgroundType:   application.BackgroundTypeSolid,
		BackgroundColour: application.NewRGBA(248, 250, 252, 255),
	})

	if err := wailsApp.Run(); err != nil {
		println("Error:", err.Error())
	}
}
