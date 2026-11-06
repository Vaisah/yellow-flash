package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app, err := NewApp()
	if err != nil {
		log.Fatal(err)
	}
	err = wails.Run(&options.App{Title: "Download Manager", Width: 1200, Height: 820, MinWidth: 900, MinHeight: 620, AssetServer: &assetserver.Options{Assets: assets}, OnStartup: app.Startup, OnShutdown: app.Shutdown, Bind: []interface{}{app}})
	if err != nil {
		log.Fatal(err)
	}
}
