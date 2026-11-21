package install

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vakaohub/launcher/internal/apperr"
)

func writeZIP(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("创建 zip 失败：%v", err)
	}
	writer := zip.NewWriter(file)
	for name, content := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("写入 zip 条目失败：%v", err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("写入 zip 内容失败：%v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 zip 失败：%v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("关闭文件失败：%v", err)
	}
	return path
}

func TestValidateWindowsZIP(t *testing.T) {
	t.Run("顶层主程序加工具目录", func(t *testing.T) {
		path := writeZIP(t, map[string]string{
			"WireDeck.exe":           "demo",
			"platform-tools/adb.exe": "tool",
			"scrcpy/scrcpy.exe":      "tool",
		})
		if err := ValidatePackage("win32", "zip", path); err != nil {
			t.Fatalf("合法 zip 被拒绝：%v", err)
		}
	})

	t.Run("只有一个顶层目录", func(t *testing.T) {
		path := writeZIP(t, map[string]string{"Demo/Demo.exe": "demo"})
		if err := ValidatePackage("win32", "zip", path); err != nil {
			t.Fatalf("单目录 zip 被拒绝：%v", err)
		}
	})

	t.Run("缺少可执行文件", func(t *testing.T) {
		path := writeZIP(t, map[string]string{"README.txt": "hi"})
		err := ValidatePackage("win32", "zip", path)
		if kind := apperr.KindOf(err); kind != apperr.KindCorrupt {
			t.Fatalf("缺少 exe => %s，期望 corrupt", kind)
		}
		if !strings.Contains(apperr.UserMessage(err), "未找到可执行文件") {
			t.Fatalf("文案不可读：%q", apperr.UserMessage(err))
		}
	})

	t.Run("多目录混杂", func(t *testing.T) {
		path := writeZIP(t, map[string]string{
			"one/App.exe": "demo",
			"two/App.exe": "demo",
		})
		err := ValidatePackage("win32", "zip", path)
		if kind := apperr.KindOf(err); kind != apperr.KindCorrupt {
			t.Fatalf("多目录混杂 => %s，期望 corrupt", kind)
		}
		if !strings.Contains(apperr.UserMessage(err), "无法确定主程序") {
			t.Fatalf("文案不可读：%q", apperr.UserMessage(err))
		}
	})

	t.Run("路径穿越", func(t *testing.T) {
		path := writeZIP(t, map[string]string{"../evil.exe": "demo"})
		err := ValidatePackage("win32", "zip", path)
		if kind := apperr.KindOf(err); kind != apperr.KindCorrupt {
			t.Fatalf("路径穿越 => %s，期望 corrupt", kind)
		}
		if !strings.Contains(apperr.UserMessage(err), "不安全的路径") {
			t.Fatalf("文案不可读：%q", apperr.UserMessage(err))
		}
	})
}

func TestValidateWindowsEXE(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.exe")
	if err := os.WriteFile(path, []byte("not a pe"), 0o644); err != nil {
		t.Fatalf("写入文件失败：%v", err)
	}
	err := ValidatePackage("win32", "exe", path)
	if kind := apperr.KindOf(err); kind != apperr.KindCorrupt {
		t.Fatalf("伪 exe => %s，期望 corrupt", kind)
	}
	if !strings.Contains(apperr.UserMessage(err), "不是有效的 Windows 可执行文件") {
		t.Fatalf("文案不可读：%q", apperr.UserMessage(err))
	}
}

func TestValidateMSI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.msi")
	if err := os.WriteFile(path, []byte("not-msi!"), 0o644); err != nil {
		t.Fatalf("写入文件失败：%v", err)
	}
	if err := ValidatePackage("win32", "msi", path); apperr.KindOf(err) != apperr.KindCorrupt {
		t.Fatalf("伪 msi => %s，期望 corrupt", apperr.KindOf(err))
	}

	valid := filepath.Join(t.TempDir(), "ok.msi")
	magic := []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}
	if err := os.WriteFile(valid, magic, 0o644); err != nil {
		t.Fatalf("写入文件失败：%v", err)
	}
	if err := ValidatePackage("win32", "msi", valid); err != nil {
		t.Fatalf("合法 msi 头被拒绝：%v", err)
	}
}

func TestValidateDarwinZIP(t *testing.T) {
	valid := writeZIP(t, map[string]string{
		"Demo.app/Contents/Info.plist": "<plist/>",
		"Demo.app/Contents/MacOS/Demo": "binary",
	})
	if err := ValidatePackage("darwin", "zip", valid); err != nil {
		t.Fatalf("合法 .app zip 被拒绝：%v", err)
	}

	invalid := writeZIP(t, map[string]string{"Demo.txt": "hi"})
	err := ValidatePackage("darwin", "zip", invalid)
	if kind := apperr.KindOf(err); kind != apperr.KindCorrupt {
		t.Fatalf("缺少 .app => %s，期望 corrupt", kind)
	}
	if !strings.Contains(apperr.UserMessage(err), "未找到完整的 .app") {
		t.Fatalf("文案不可读：%q", apperr.UserMessage(err))
	}
}

func TestValidateUnsupportedFormat(t *testing.T) {
	err := ValidatePackage("win32", "dmg", filepath.Join(t.TempDir(), "x.dmg"))
	if kind := apperr.KindOf(err); kind != apperr.KindUnsupported {
		t.Fatalf("不支持的格式 => %s，期望 unsupported", kind)
	}
}
