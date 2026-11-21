package main

import (
	"embed"
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/logging"
	"vakaohub/launcher/internal/service"
	launcherversion "vakaohub/launcher/internal/version"
)

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	application.RegisterEvent[service.Progress]("install:progress")
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(launcherversion.Version)
		return
	}

	if path, err := logging.Setup(); err != nil {
		log.Printf("初始化日志失败：%v", err)
	} else {
		slog.Info("launcher started", "version", launcherversion.Version, "log", path)
	}

	app := application.New(application.Options{
		Name:         "VaKao Hub",
		Description:  "VaKao Hub Launcher",
		MarshalError: apperr.Marshal,
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	host := service.NewHost(app)
	app.RegisterService(application.NewService(service.NewCatalog(host)))
	app.RegisterService(application.NewService(service.NewInstall(host)))
	app.RegisterService(application.NewService(service.NewSettings(host)))
	app.RegisterService(application.NewService(service.NewLauncher(host)))

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "VaKao Hub",
		Width:            1200,
		Height:           800,
		MinWidth:         980,
		MinHeight:        620,
		BackgroundColour: application.NewRGB(243, 241, 236),
		URL:              "/",
		Mac: application.MacWindow{
			TitleBar:                application.MacTitleBarHiddenInset,
			InvisibleTitleBarHeight: 36,
		},
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
