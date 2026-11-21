//go:build darwin

package selfupdate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/install"
)

func stagePayload(ctx context.Context, format, filePath, stageDir, _ string) (string, error) {
	return install.ExtractApp(ctx, format, filePath, stageDir)
}

func validatePayload(ctx context.Context, appPath string) error {
	if err := ctx.Err(); err != nil {
		return apperr.FromContext(err, "selfupdate.verify")
	}
	infoPath := filepath.Join(appPath, "Contents", "Info.plist")
	if _, err := os.Stat(infoPath); err != nil {
		return apperr.New(apperr.KindCorrupt, "selfupdate.verify", "更新包内未找到有效的 .app 结构", err)
	}
	executable := bundleExecutable(ctx, infoPath)
	if executable != "" {
		target := filepath.Join(appPath, "Contents", "MacOS", executable)
		info, err := os.Stat(target)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			return apperr.New(apperr.KindCorrupt, "selfupdate.verify", "更新包内可执行文件无效", err)
		}
	} else {
		entries, err := os.ReadDir(filepath.Join(appPath, "Contents", "MacOS"))
		if err != nil || len(entries) == 0 {
			return apperr.New(apperr.KindCorrupt, "selfupdate.verify", "更新包内未找到可执行文件", err)
		}
	}

	output, err := exec.CommandContext(ctx, "codesign", "--verify", "--deep", "--strict", appPath).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return apperr.FromContext(ctx.Err(), "selfupdate.signature")
		}
		return apperr.New(
			apperr.KindSignature,
			"selfupdate.signature",
			"",
			fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output))),
		)
	}
	return nil
}

func bundleExecutable(ctx context.Context, infoPath string) string {
	output, err := exec.CommandContext(
		ctx, "plutil", "-extract", "CFBundleExecutable", "raw", "-o", "-", infoPath,
	).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
