package install

import (
	"os"
	"path/filepath"
	"strings"
)

// ExpandDir 展开用户输入的 "~"，并清理多余分隔符。
// 空串保持为空，由调用方按平台默认目录处理。
func ExpandDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return dir
	}
	if dir == "~" {
		return home
	}
	if strings.HasPrefix(dir, "~/") || strings.HasPrefix(dir, "~"+string(os.PathSeparator)) {
		return filepath.Join(home, dir[2:])
	}
	return filepath.Clean(dir)
}
