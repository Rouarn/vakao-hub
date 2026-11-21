package install

import "strings"

// normalizeName 归一化应用名，用于跨平台匹配已安装记录与目录名。
func normalizeName(value string) string {
	replacer := strings.NewReplacer(" ", "", "-", "", "_", "", ".", "")
	return strings.ToLower(replacer.Replace(value))
}
