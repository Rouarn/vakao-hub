package selfupdate

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"vakaohub/launcher/internal/apperr"
)

//go:embed all:bin
var helperFS embed.FS

// extractHelper 把当前平台的 helper 解到工作目录，chmod 0755。
func extractHelper(destDir string) (string, error) {
	name := helperName()
	data, err := helperFS.ReadFile("bin/" + name)
	if err != nil {
		return "", apperr.New(
			apperr.KindInternal,
			"selfupdate.helper",
			"更新组件缺失，请重新安装",
			err,
		)
	}
	path := filepath.Join(destDir, filepath.Base(name))
	if err := os.WriteFile(path, data, 0o755); err != nil {
		return "", apperr.FromOS(err, "selfupdate.helper")
	}
	return path, nil
}

func helperName() string {
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	return fmt.Sprintf("%s_%s/vakaohub-updater%s", runtime.GOOS, runtime.GOARCH, suffix)
}
