//go:build !darwin && !windows

package selfupdate

import (
	"context"

	"vakaohub/launcher/internal/apperr"
)

func stagePayload(_ context.Context, format, _, _, _ string) (string, error) {
	return "", apperr.New(
		apperr.KindUnsupported,
		"selfupdate.format",
		"当前平台暂不支持启动器自动更新，请手动下载安装",
		nil,
	)
}

func validatePayload(_ context.Context, _ string) error {
	return apperr.New(
		apperr.KindUnsupported,
		"selfupdate.verify",
		"当前平台暂不支持启动器自动更新，请手动下载安装",
		nil,
	)
}
