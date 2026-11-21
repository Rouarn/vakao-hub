package install

import "strings"

// registryEntry 是 Windows 卸载注册表项里与启动路径相关的字段集合。
type registryEntry struct {
	DisplayName     string
	DisplayVersion  string
	InstallLocation string
	DisplayIcon     string
}

// cleanExecutablePath 去掉引号与 `,0` 形式的图标索引后缀。
func cleanExecutablePath(raw string) string {
	value := strings.TrimSpace(raw)
	if index := strings.Index(value, ","); index >= 0 {
		value = value[:index]
	}
	return strings.Trim(strings.TrimSpace(value), `"`)
}

// isExecutableName 判断路径是否以 .exe 结尾。
func isExecutableName(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".exe")
}

// resolveLaunchPath 优先使用 DisplayIcon 指向的 exe，其次在 InstallLocation 中查找；
// 两者都找不到可执行文件时返回空串，调用方据此跳过该注册表项。
func resolveLaunchPath(entry registryEntry, exists func(string) bool, findExe func(string) (string, bool)) string {
	if icon := cleanExecutablePath(entry.DisplayIcon); icon != "" && isExecutableName(icon) && exists(icon) {
		return icon
	}

	location := cleanExecutablePath(entry.InstallLocation)
	if location == "" {
		return ""
	}
	if isExecutableName(location) && exists(location) {
		return location
	}
	if exe, ok := findExe(location); ok {
		return exe
	}
	return ""
}
