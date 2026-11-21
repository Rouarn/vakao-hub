//go:build darwin

package install

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/contract"
)

func TestValidateInstallDirReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 不受目录权限限制")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("获取用户目录失败：%v", err)
	}
	dir, err := os.MkdirTemp(home, ".vakaohub-readonly-*")
	if err != nil {
		t.Fatalf("创建测试目录失败：%v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o755)
		_ = os.RemoveAll(dir)
	})
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("设置只读权限失败：%v", err)
	}

	err = ValidateInstallDir(dir)
	if apperr.KindOf(err) != apperr.KindPermission {
		t.Fatalf("只读目录 Kind = %s，期望 permission（%v）", apperr.KindOf(err), err)
	}
}

func TestExtractAppCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ExtractApp(ctx, contract.FormatZIP, "missing.zip", t.TempDir())
	if apperr.KindOf(err) != apperr.KindCanceled {
		t.Fatalf("取消解压 Kind = %s，期望 canceled（%v）", apperr.KindOf(err), err)
	}
}

func TestExtractAppCorruptZip(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "broken.zip")
	if err := os.WriteFile(archive, []byte("not a zip"), 0o644); err != nil {
		t.Fatalf("写入坏包失败：%v", err)
	}

	_, err := ExtractApp(context.Background(), contract.FormatZIP, archive, filepath.Join(root, "out"))
	if apperr.KindOf(err) != apperr.KindCorrupt {
		t.Fatalf("坏包 Kind = %s，期望 corrupt（%v）", apperr.KindOf(err), err)
	}
}

func TestInstallUnsupportedFormat(t *testing.T) {
	_, err := Install(context.Background(), contract.FormatAPK, "app.apk", "demo", "Demo", "", "")
	if apperr.KindOf(err) != apperr.KindUnsupported {
		t.Fatalf("不支持格式 Kind = %s，期望 unsupported（%v）", apperr.KindOf(err), err)
	}
}
