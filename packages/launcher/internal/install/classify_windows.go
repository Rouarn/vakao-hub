//go:build windows

package install

import (
	"debug/pe"
	"io"
	"os"
	"path/filepath"
	"strings"

	"vakaohub/launcher/internal/contract"
)

// Classify 判断 Windows 产物该复制托管还是交给它自己的安装器。
// 判据顺序：格式 -> 安装器标记/节名 -> 便携框架标记 -> 文件名启发式 -> 不确定。
func Classify(format, filePath string) Classification {
	switch format {
	case contract.FormatMSI:
		return Classification{Kind: KindInstaller, Confidence: ConfidenceCertain, Reason: "msi"}
	case contract.FormatEXE:
		return classifyEXE(filePath)
	default:
		// zip 由安装流程解压后再对主程序分类，这里不预设结论。
		return Classification{Kind: KindPortable, Confidence: ConfidenceCertain, Reason: "format:" + format}
	}
}

func classifyEXE(path string) Classification {
	data, err := sampleExecutable(path)
	if err != nil {
		// 读不到文件时按安装器处理：让后续调起给出可诊断的错误，而不是误复制一份。
		return Classification{Kind: KindInstaller, Confidence: ConfidenceCertain, Reason: "unreadable"}
	}
	if marker, ok := containsAnyMarker(data, installerMarkers); ok {
		return Classification{Kind: KindInstaller, Confidence: ConfidenceCertain, Reason: "marker:" + marker}
	}
	if name := installerSection(path); name != "" {
		return Classification{Kind: KindInstaller, Confidence: ConfidenceCertain, Reason: "section:" + name}
	}
	if marker, ok := containsAnyMarker(data, portableMarkers); ok {
		return Classification{Kind: KindPortable, Confidence: ConfidenceCertain, Reason: "marker:" + marker}
	}
	if looksLikeInstallerName(filepath.Base(path)) {
		return Classification{Kind: KindInstaller, Confidence: ConfidenceCertain, Reason: "name-heuristic"}
	}
	return Classification{Kind: KindPortable, Confidence: ConfidenceUncertain, Reason: "no-installer-marker"}
}

// sampleExecutable 读取文件头尾各 4 MiB；小于 64 MiB 时整文件读取，
// 避免把安装器签名放在中段或尾部 overlay 时漏判。
func sampleExecutable(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	const window = 4 << 20
	if info.Size() <= 64<<20 {
		return io.ReadAll(file)
	}

	head := make([]byte, window)
	n, err := io.ReadFull(file, head)
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	data := append([]byte(nil), head[:n]...)

	tail := make([]byte, window)
	if _, err := file.ReadAt(tail, info.Size()-window); err == nil {
		data = append(data, tail...)
	}
	return data, nil
}

// installerSection 读取 PE 节名，NSIS 的 .ndata 与 WiX Burn 的 .wixburn 只属于安装器。
func installerSection(path string) string {
	file, err := pe.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	for _, section := range file.Sections {
		name := strings.ToLower(section.Name)
		for _, marker := range installerSectionNames {
			if strings.Contains(name, strings.TrimPrefix(marker, ".")) {
				return section.Name
			}
		}
	}
	return ""
}

// PreviewTargetDir 提前算出便携模式的落点，供「不确定」提示文案使用。
func PreviewTargetDir(appKey, targetPath, installDir string) string {
	if container, ok := containerOf(targetPath, appKey); ok {
		return container
	}
	if root := installRoot(installDir); root != "" {
		return filepath.Join(root, appKey)
	}
	return ""
}
