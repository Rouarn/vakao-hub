//go:build windows

package selfupdate

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/contract"
)

func stagePayload(ctx context.Context, format, filePath, stageDir, name string) (string, error) {
	switch format {
	case contract.FormatEXE:
		target := filepath.Join(stageDir, name)
		if err := copyFile(ctx, filePath, target); err != nil {
			return "", err
		}
		return target, nil
	case contract.FormatZIP:
		reader, err := zip.OpenReader(filePath)
		if err != nil {
			return "", apperr.New(apperr.KindCorrupt, "selfupdate.zip", "更新包损坏，无法解压", err)
		}
		defer reader.Close()

		for _, file := range reader.File {
			if err := ctx.Err(); err != nil {
				return "", apperr.FromContext(err, "selfupdate.zip")
			}
			if file.FileInfo().IsDir() || !strings.HasSuffix(strings.ToLower(file.Name), ".exe") {
				continue
			}
			target := filepath.Join(stageDir, filepath.Base(file.Name))
			if err := extractMember(ctx, file, target); err != nil {
				return "", err
			}
			return target, nil
		}
		return "", apperr.New(
			apperr.KindCorrupt,
			"selfupdate.zip",
			"更新包内未找到可执行文件（.exe）",
			nil,
		)
	default:
		return "", apperr.New(
			apperr.KindUnsupported,
			"selfupdate.format",
			fmt.Sprintf("启动器暂不支持 %s 格式自动更新，请手动下载安装", format),
			nil,
		)
	}
}

func validatePayload(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return apperr.FromContext(err, "selfupdate.verify")
	}
	file, err := os.Open(path)
	if err != nil {
		return apperr.New(apperr.KindCorrupt, "selfupdate.verify", "更新包内未找到可执行文件", err)
	}
	defer file.Close()

	header := make([]byte, 2)
	if _, err := file.Read(header); err != nil {
		return apperr.New(apperr.KindCorrupt, "selfupdate.verify", "读取可执行文件头失败", err)
	}
	if header[0] != 'M' || header[1] != 'Z' {
		return apperr.New(apperr.KindCorrupt, "selfupdate.verify", "更新包不是有效的 Windows 可执行文件", nil)
	}
	return nil
}

func copyFile(ctx context.Context, source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return apperr.FromOS(err, "selfupdate.copy")
	}
	defer in.Close()

	out, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return apperr.FromOS(err, "selfupdate.copy")
	}
	defer out.Close()

	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return apperr.FromContext(err, "selfupdate.copy")
		}
		read, readErr := in.Read(buffer)
		if read > 0 {
			if _, err := out.Write(buffer[:read]); err != nil {
				return apperr.FromOS(err, "selfupdate.copy")
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return apperr.FromOS(readErr, "selfupdate.copy")
		}
	}
}

func extractMember(ctx context.Context, file *zip.File, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return apperr.FromOS(err, "selfupdate.zip")
	}
	source, err := file.Open()
	if err != nil {
		return apperr.New(apperr.KindCorrupt, "selfupdate.zip", "更新包损坏，无法解压", err)
	}
	defer source.Close()

	destination, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return apperr.FromOS(err, "selfupdate.zip")
	}
	defer destination.Close()

	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return apperr.FromContext(err, "selfupdate.zip")
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			if _, err := destination.Write(buffer[:read]); err != nil {
				return apperr.FromOS(err, "selfupdate.zip")
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return apperr.FromOS(readErr, "selfupdate.zip")
		}
	}
}
