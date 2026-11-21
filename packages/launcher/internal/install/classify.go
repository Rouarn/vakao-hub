package install

import (
	"bytes"
	"encoding/binary"
	"unicode/utf16"
)

// Kind 描述 Windows 产物的安装方式：托管复制，还是交给它自己的安装器。
type Kind string

const (
	KindPortable  Kind = "portable"
	KindInstaller Kind = "installer"
)

// Confidence 表示分类结果的可信度；uncertain 时安装前必须让用户二选一。
type Confidence string

const (
	ConfidenceCertain   Confidence = "certain"
	ConfidenceUncertain Confidence = "uncertain"
)

// Classification 是分类器输出，Reason 会写进安装记录，便于事后排查误判。
type Classification struct {
	Kind       Kind
	Confidence Confidence
	Reason     string
}

// 安装方式由前端在「不确定」时二选一后回传；空串表示自动判断。
const (
	ModeAuto      = ""
	ModePortable  = "portable"
	ModeInstaller = "installer"
)

// installerMarkers 是常见安装器写在文件里的签名。
// 同时按 ASCII 与 UTF-16LE 匹配：PE 版本资源里的描述串是 UTF-16。
var installerMarkers = []string{
	"NullsoftInst",       // NSIS（electron-builder / Tauri 默认）
	"Inno Setup",         // Inno Setup
	"InnoSetupLdr",       // Inno Setup 引导器
	"WixBundle",          // WiX Burn
	"InstallShield",      // InstallShield
	"Squirrel",           // Electron 自动更新安装器
	";!@Install@!UTF-8!", // 7-Zip 自解压
	"7zS.sfx",            // 7-Zip 自解压
	"WinRAR SFX",         // WinRAR 自解压
	"Advanced Installer", // Advanced Installer
	"Setup Factory",      // Setup Factory
	"InstallAware",       // InstallAware
	"InstallAnywhere",    // InstallAnywhere
}

// portableMarkers 是明确的便携/单文件框架标记。
// 有安装器标记时以安装器为准；只在没有安装器标记时用来压低「名字像安装器」的误判。
var portableMarkers = []string{
	"MEIPASS",        // PyInstaller
	"PYINSTALLER",    // PyInstaller
	"app.asar",       // Electron 便携版
	"__TAURI",        // Tauri
	"tauri://",       // Tauri
	"Go build ID",    // Go 单文件
	".go.buildinfo",  // Go 单文件节名
	".netcorebundle", // .NET 单文件
}

// installerSectionNames 是只有安装器才会出现的 PE 节名，比字符串更稳。
var installerSectionNames = []string{".ndata", ".wixburn"}

func containsAnyMarker(data []byte, markers []string) (string, bool) {
	for _, marker := range markers {
		ascii := []byte(marker)
		if bytes.Contains(data, ascii) {
			return marker, true
		}
		if encoded := utf16LE(marker); bytes.Contains(data, encoded) {
			return marker, true
		}
	}
	return "", false
}

// utf16LE 把字符串编码为小端 UTF-16，用于匹配 PE 版本资源。
func utf16LE(value string) []byte {
	units := utf16.Encode([]rune(value))
	buffer := make([]byte, len(units)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(buffer[index*2:], unit)
	}
	return buffer
}

// looksLikeInstallerName 只在没有任何强信号时使用：
// 文件名自称 setup/install/installer/安装，基本可以认为它是安装向导。
func looksLikeInstallerName(name string) bool {
	lower := bytes.ToLower([]byte(name))
	for _, token := range []string{"setup", "install", "安装"} {
		if bytes.Contains(lower, []byte(token)) {
			return true
		}
	}
	return false
}
