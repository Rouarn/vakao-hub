package selfupdate

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"vakaohub/launcher/internal/apperr"
)

//go:embed all:bin
var helperFS embed.FS

// helperAssets 允许测试替换内嵌资源；生产环境固定指向 helperFS。
var helperAssets fs.FS = helperFS

// extractHelper 把当前平台的 helper 解到工作目录。
func extractHelper(destDir string) (string, error) {
	return extractHelperFrom(helperAssets, runtime.GOOS, runtime.GOARCH, destDir)
}

// extractHelperFrom 按 goos/goarch 取 helper 并落盘，文件名始终使用规范名
// （Windows 必须带 .exe，否则 CreateProcess 无法直接执行）。
func extractHelperFrom(assets fs.FS, goos, goarch, destDir string) (string, error) {
	name := helperNameFor(goos, goarch)
	data, err := readHelper(assets, name)
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

// readHelper 优先读规范名，再兼容早期交叉编译产物：在 macOS 上交叉编译 Windows 时，
// Task 的 platforms 判定走的是宿主系统，helper 曾以没有 .exe 的名字落盘，
// 导致 Windows 端解不出更新组件。这里保留后缀回退，避免历史产物立刻不可用。
func readHelper(assets fs.FS, name string) ([]byte, error) {
	data, err := fs.ReadFile(assets, "bin/"+name)
	if err == nil {
		return data, nil
	}
	fallback := strings.TrimSuffix(name, ".exe")
	if fallback == name {
		return nil, err
	}
	alt, altErr := fs.ReadFile(assets, "bin/"+fallback)
	if altErr != nil {
		return nil, err
	}
	return alt, nil
}

func helperName() string {
	return helperNameFor(runtime.GOOS, runtime.GOARCH)
}

func helperNameFor(goos, goarch string) string {
	suffix := ""
	if goos == "windows" {
		suffix = ".exe"
	}
	return fmt.Sprintf("%s_%s/vakaohub-updater%s", goos, goarch, suffix)
}
