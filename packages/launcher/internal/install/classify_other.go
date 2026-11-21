//go:build !windows

package install

// Classify 在非 Windows 平台只用于编译与调用一致性；
// macOS/Linux 的产物格式不会走到 Windows 安装器分支。
func Classify(string, string) Classification {
	return Classification{Kind: KindPortable, Confidence: ConfidenceCertain, Reason: "platform"}
}

// PreviewTargetDir 返回「不确定时」提示给用户的落点。
func PreviewTargetDir(_ string, _, installDir string) string {
	if dir := ExpandDir(installDir); dir != "" {
		return dir
	}
	return DefaultInstallDir()
}
