module github.com/CodeNeow/MyLlama

go 1.25.0

require (
	// fyne.io/systray — Windows system tray (core/tray_windows.go).
	// Floor = last verified v1.12.2; ceiling = v2 (next major, different API).
	// What breaks if it moves: our tray lifecycle is designed around this
	// version's package-level quitOnce and its non-resettable window-class /
	// message-loop state — systray.Run can never be re-called in one process.
	// The trayStarted/QuitTray idempotency and the "re-enabling the tray
	// requires an app restart" rule in tray_windows.go assume exactly that
	// behavior; a version that resets internal state (or changes Run/Quit
	// semantics) would turn the guard into a silent no-op or a crash.
	fyne.io/systray v1.12.2

	// golang.org/x/sys — direct Windows syscalls: single-instance named mutex
	// (CreateMutex/WaitForSingleObject/OpenProcess, core/singleinstance_windows.go),
	// GetDiskFreeSpaceEx (core/diskusage_windows.go), ShellExecute "runas"
	// elevation (core/installer_launch_windows.go), native MessageBoxW alert
	// via NewLazySystemDLL + UTF16PtrFromString (core/headlessalert_windows.go),
	// and UTF-16 helpers for the locale probe. A floor, not a ceiling: x/sys
	// keeps backward compatibility within v0, so bump freely — never let it
	// drop below what wails/v3 requires (MVS picks the max anyway).
	golang.org/x/sys v0.46.0
)

// github.com/wailsapp/wails/v3 — app shell (window, bindings, assets).
// The /v3 module path is a rewrite (not an upgrade of /v2); pinned to the
// tagged beta the migration targets. What breaks if it moves: the v3
// service registry binds core.App (frontend/src/wails.ts re-exports the
// generated frontend/bindings package, which must be regenerated with
// `wails3 generate bindings` whenever bound methods change), main.go wires
// the go:embed frontend/dist asset handler plus the frameless window and
// the ServiceStartup/ServiceShutdown lifecycle against v3 behavior — bump
// only together with a frontend rebuild and the full quality gates.
require github.com/wailsapp/wails/v3 v3.0.0-beta.16

require (
	github.com/adrg/xdg v0.5.3 // indirect
	github.com/coder/websocket v1.8.14 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
)
