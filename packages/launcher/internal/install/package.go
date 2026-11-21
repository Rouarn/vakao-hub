package install

import (
	"archive/zip"
	"debug/pe"
	"fmt"
	"os"
	"path"
	"strings"

	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/contract"
)

// 支持的包形态约定（安装端与发布端共用，ship.sh 会调用同一份校验）：
//   win32 + exe：必须是有效的 PE，GUI/Console 子系统均可；
//     没有安装器标记时按便携版复制到 <安装目录>/<appKey>/，有 NSIS/Inno 等标记时运行安装器。
//   win32 + msi：必须是 OLE 复合文档（微软安装包）。
//   win32 + zip：至少有一个 .exe；顶层有 exe 时以它为主程序，否则只允许一个顶层目录。
//   darwin + zip：必须包含 .app/Contents/Info.plist 与 .app/Contents/MacOS/ 下的可执行文件。
// 任何路径穿越、绝对路径或缺少主程序都会在解压/调起安装器之前失败。

// ValidatePackage 在安装前检查产物结构，返回可以直接展示给用户的原因。
func ValidatePackage(platform, format, filePath string) error {
	switch platform {
	case contract.PlatformWindows:
		switch format {
		case contract.FormatEXE:
			return validateWindowsEXE(filePath)
		case contract.FormatMSI:
			return validateMSI(filePath)
		case contract.FormatZIP:
			return validateWindowsZIP(filePath)
		default:
			return apperr.New(
				apperr.KindUnsupported,
				"package.format",
				fmt.Sprintf("Windows 暂不支持 %s 格式", format),
				nil,
			)
		}
	case contract.PlatformDarwin:
		switch format {
		case contract.FormatZIP:
			return validateDarwinZIP(filePath)
		case contract.FormatDMG:
			// dmg 由系统挂载，结构由系统校验。
			return nil
		default:
			return apperr.New(
				apperr.KindUnsupported,
				"package.format",
				fmt.Sprintf("macOS 暂不支持 %s 格式", format),
				nil,
			)
		}
	case contract.PlatformLinux:
		return nil
	default:
		return nil
	}
}

func validateWindowsEXE(filePath string) error {
	file, err := pe.Open(filePath)
	if err != nil {
		return corruptPackage("安装包不是有效的 Windows 可执行文件，请删除后重新下载", err)
	}
	defer file.Close()

	var subsystem uint16
	switch header := file.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		subsystem = header.Subsystem
	case *pe.OptionalHeader64:
		subsystem = header.Subsystem
	default:
		return corruptPackage("安装包缺少有效的 PE 可选头，请删除后重新下载", nil)
	}
	if subsystem != 2 && subsystem != 3 {
		return corruptPackage("安装包不是可运行的 Windows 程序（子系统未知），请重新打包", nil)
	}
	return nil
}

func validateMSI(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return apperr.FromOS(err, "package.inspect")
	}
	defer file.Close()

	header := make([]byte, 8)
	if _, err := file.Read(header); err != nil {
		return corruptPackage("安装包不完整，请删除后重新下载", err)
	}
	// OLE 复合文档头：D0 CF 11 E0 A1 B1 1A E1
	expected := []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}
	if string(header) != string(expected) {
		return corruptPackage("安装包不是有效的 MSI 安装包，请删除后重新下载", nil)
	}
	return nil
}

func validateWindowsZIP(filePath string) error {
	reader, err := zip.OpenReader(filePath)
	if err != nil {
		return corruptPackage("安装包不是有效的 ZIP 文件，请删除后重新下载", err)
	}
	defer reader.Close()

	rootExe := ""
	var rootExeSize uint64
	var nestedExeDirs = make(map[string]bool)
	exeCount := 0
	for _, entry := range reader.File {
		if err := validateEntryName(entry.Name); err != nil {
			return err
		}
		if entry.FileInfo().IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name), ".exe") {
			continue
		}
		exeCount++
		clean := strings.Trim(strings.ReplaceAll(entry.Name, "\\", "/"), "/")
		if !strings.Contains(clean, "/") {
			if rootExe == "" || entry.UncompressedSize64 > rootExeSize {
				rootExe = clean
				rootExeSize = entry.UncompressedSize64
			}
			continue
		}
		nestedExeDirs[strings.SplitN(clean, "/", 2)[0]] = true
	}

	if exeCount == 0 {
		return corruptPackage("安装包内未找到可执行文件（.exe），请重新打包或联系应用作者", nil)
	}
	// 顶层没有 exe 时，所有 exe 必须位于同一个顶层目录，避免多版本/工具集混杂。
	if rootExe == "" && len(nestedExeDirs) > 1 {
		return corruptPackage(
			"安装包内多个目录都包含可执行文件，无法确定主程序；请在压缩包顶层放主程序，或只保留一个顶层目录",
			nil,
		)
	}
	return nil
}

func validateDarwinZIP(filePath string) error {
	reader, err := zip.OpenReader(filePath)
	if err != nil {
		return corruptPackage("安装包不是有效的 ZIP 文件，请删除后重新下载", err)
	}
	defer reader.Close()

	hasInfoPlist := false
	hasMacOSBinary := false
	for _, entry := range reader.File {
		if err := validateEntryName(entry.Name); err != nil {
			return err
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		clean := strings.Trim(strings.ReplaceAll(entry.Name, "\\", "/"), "/")
		if strings.HasSuffix(clean, ".app/Contents/Info.plist") {
			hasInfoPlist = true
		}
		if strings.Contains(clean, ".app/Contents/MacOS/") {
			hasMacOSBinary = true
		}
	}
	if !hasInfoPlist || !hasMacOSBinary {
		return corruptPackage("安装包内未找到完整的 .app（缺少 Info.plist 或 MacOS 可执行文件），请重新打包", nil)
	}
	return nil
}

func validateEntryName(name string) error {
	clean := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) || strings.Contains(clean, ":") {
		return corruptPackage(fmt.Sprintf("安装包内含不安全的路径 %q，已阻止安装", name), nil)
	}
	return nil
}

func corruptPackage(message string, cause error) error {
	return apperr.New(apperr.KindCorrupt, "package.inspect", message, cause)
}
