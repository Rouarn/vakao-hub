//go:build windows

package install

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/windows/registry"

	"vakaohub/launcher/internal/version"
)

// Detected 定义在 cache.go。

// CurrentBundle 返回启动器可执行文件路径与构建注入的版本；
// 开发构建未注入版本时返回 false，避免空版本参与更新判断。
func CurrentBundle() (string, string, bool) {
	if version.Version == "" {
		return "", "", false
	}
	executable, err := os.Executable()
	if err != nil {
		return "", "", false
	}
	return executable, version.Version, true
}

var uninstallCache scanCache[[]registryEntry]

// FindApps 从缓存的卸载注册表索引中按显示名匹配已安装程序。
func FindApps(name string) []Detected {
	target := normalizeName(name)
	entries := uninstallCache.get(queryUninstallRegistry)
	found := make([]Detected, 0, 2)

	for _, entry := range entries {
		if normalizeName(entry.DisplayName) != target {
			continue
		}
		path := resolveLaunchPath(entry, fileExists, findFirstExecutable)
		if path == "" {
			continue
		}
		found = append(found, Detected{Path: path, Version: entry.DisplayVersion})
	}

	sort.Slice(found, func(i, j int) bool { return found[i].Path < found[j].Path })
	return found
}

// InvalidateIndex 在安装/卸载后调用，下一次 FindApps 重新读取注册表。
func InvalidateIndex() {
	uninstallCache.reset()
}

// uninstallRegistryRoots 与 reg query 的三条路径一一对应：
// 64 位 HKLM、WOW6432Node 下的 32 位 HKLM、以及当前用户 HKCU。
var uninstallRegistryRoots = []struct {
	hive registry.Key
	path string
}{
	{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
	{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
	{registry.CURRENT_USER, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
}

// queryUninstallRegistry 直接读注册表，不经 cmd.exe。
// 启动器是 GUI 子系统，没有控制台；早先通过 `cmd /c reg query` 读取时，
// 每个根键都会新建一个控制台窗口（启动时闪屏），因此这里改成进程内读取。
func queryUninstallRegistry() []registryEntry {
	entries := make([]registryEntry, 0, 16)
	for _, root := range uninstallRegistryRoots {
		key, err := registry.OpenKey(root.hive, root.path, registry.READ)
		if err != nil {
			continue
		}
		names, err := key.ReadSubKeyNames(-1)
		if err == nil {
			for _, name := range names {
				sub, err := registry.OpenKey(key, name, registry.READ)
				if err != nil {
					continue
				}
				entries = append(entries, readUninstallEntry(sub))
				sub.Close()
			}
		}
		key.Close()
	}
	return entries
}

// readUninstallEntry 取与启动路径相关的四个字符串值。
// GetStringValue 接受 REG_SZ 与 REG_EXPAND_SZ，且和 reg query 一样保留未展开的内容，
// 因此匹配结果与旧实现一致。
func readUninstallEntry(key registry.Key) registryEntry {
	return registryEntry{
		DisplayName:     registryString(key, "DisplayName"),
		DisplayVersion:  registryString(key, "DisplayVersion"),
		InstallLocation: registryString(key, "InstallLocation"),
		DisplayIcon:     registryString(key, "DisplayIcon"),
	}
}

func registryString(key registry.Key, name string) string {
	value, _, err := key.GetStringValue(name)
	if err != nil {
		return ""
	}
	return value
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// findFirstExecutable 在安装目录最多两层内查找第一个 exe，避免遍历整棵程序目录。
func findFirstExecutable(dir string) (string, bool) {
	root := filepath.Clean(dir)
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", false
	}

	found := ""
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || found != "" {
			return nil
		}
		if entry.IsDir() {
			relative := strings.TrimPrefix(path, root)
			if path != root && strings.Count(relative, string(os.PathSeparator)) >= 2 {
				return fs.SkipDir
			}
			return nil
		}
		if isExecutableName(path) {
			found = path
		}
		return nil
	})
	return found, found != ""
}
