package install

import (
	"archive/zip"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/atomicfs"
)

// commitStage 把暂存内容原子换入目标目录：换入后目标不存在则回滚，成功则清理旧备份。
// 各平台安装实现共用，避免同一段回滚逻辑分散多份。
func commitStage(staged, target string) error {
	rollback, err := swapDirectory(staged, target)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = rollback()
		}
	}()

	if _, err := os.Stat(target); err != nil {
		return apperr.FromOS(err, "install.swap")
	}
	committed = true
	if err := atomicfs.CleanupBackups(target); err != nil {
		slog.Warn("清理旧版本备份失败", "target", target, "error", err)
	}
	return nil
}

// extractZipTo 把压缩包解到暂存目录；任一步失败只留下暂存目录，旧版本不受影响。
func extractZipTo(ctx context.Context, filePath, stage string) error {
	reader, err := zip.OpenReader(filePath)
	if err != nil {
		return apperr.New(apperr.KindCorrupt, "install.zip", "", err)
	}
	defer reader.Close()

	for _, file := range reader.File {
		if err := ctx.Err(); err != nil {
			return apperr.FromContext(err, "install.zip")
		}
		name := filepath.Join(stage, file.Name)
		if !strings.HasPrefix(name, stage+string(os.PathSeparator)) {
			return apperr.New(apperr.KindCorrupt, "install.zip", "安装包包含非法路径，请重试", nil)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(name, 0o755); err != nil {
				return apperr.FromOS(err, "install.zip")
			}
			continue
		}
		if err := extractZipMember(ctx, file, name); err != nil {
			return err
		}
	}
	return nil
}

// extractZipMember 解压单个成员并创建父目录；复制过程检查 ctx。
func extractZipMember(ctx context.Context, file *zip.File, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return apperr.FromOS(err, "install.zip")
	}
	source, err := file.Open()
	if err != nil {
		return apperr.New(apperr.KindCorrupt, "install.zip", "", err)
	}
	defer source.Close()

	destination, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.Mode())
	if err != nil {
		return apperr.FromOS(err, "install.zip")
	}
	defer destination.Close()

	if err := copyWithContext(ctx, destination, source); err != nil {
		return apperr.FromContext(err, "install.zip")
	}
	return nil
}
