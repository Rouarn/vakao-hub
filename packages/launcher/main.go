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
	app.RegisterService(bindService(service.NewCatalog(host)))
	app.RegisterService(bindService(service.NewInstall(host)))
	app.RegisterService(bindService(service.NewSettings(host)))
	app.RegisterService(bindService(service.NewLauncher(host)))

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

// bindService 把服务统一注册为 apperr 线格式。
// Wails v3 的 application.Options.MarshalError 不会应用到 RegisterService 注册的服务，
// 必须在每个服务自己的选项上设置，否则返回给前端的是 Go 结构体默认字段名（PascalCase）。
func bindService[T any](instance *T) application.Service {
	return application.NewServiceWithOptions(instance, application.ServiceOptions{MarshalError: apperr.Marshal})
}
