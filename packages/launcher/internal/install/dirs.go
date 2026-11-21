package install

import (
	"os"
	"path/filepath"
	"strings"
)

// IsDirWritable 通过写入再删除临时文件探测目录是否可写。
func IsDirWritable(dir string) bool {
	probe, err := os.CreateTemp(dir, ".vakaohub-write-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	probe.Close()
	_ = os.Remove(name)
	return true
}

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
