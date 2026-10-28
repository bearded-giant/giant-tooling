package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Create an instance of the app structure
	app := NewApp()

	// Create application with options
	err := wails.Run(&options.App{
		Title:     "giantmem",
		Width:     1280,
		Height:    820,
		MinWidth:  900,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		Menu:             appMenu(app),
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Mac: &mac.Options{
			OnUrlOpen: app.onURLOpen,
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}

// the AppMenu role is native and closed to extra items, so Settings needs a hand-built app menu
func appMenu(app *App) *menu.Menu {
	emit := func(name string) menu.Callback {
		return func(*menu.CallbackData) { runtime.EventsEmit(app.ctx, name) }
	}
	m := menu.NewMenu()
	am := m.AddSubmenu("giantmem")
	am.AddText("About giantmem", nil, emit("menu:about"))
	am.AddSeparator()
	am.AddText("Settings…", keys.CmdOrCtrl(","), emit("menu:settings"))
	am.AddSeparator()
	am.AddText("Hide giantmem", keys.CmdOrCtrl("h"), func(*menu.CallbackData) { runtime.Hide(app.ctx) })
	am.AddText("Hide Others", keys.Combo("h", keys.CmdOrCtrlKey, keys.OptionOrAltKey), func(*menu.CallbackData) { hideOthers() })
	am.AddText("Show All", nil, func(*menu.CallbackData) { showAll() })
	am.AddSeparator()
	am.AddText("Quit giantmem", keys.CmdOrCtrl("q"), func(*menu.CallbackData) { runtime.Quit(app.ctx) })
	m.Append(menu.EditMenu())
	m.Append(menu.WindowMenu())
	return m
}
