package service

import (
	"context"

	"vakaohub/launcher/internal/selfupdate"
)

// selfUpdate 走统一的自更新流程：暂存并校验新包，启动脱离进程的 helper；
// helper 在本进程退出后完成换入、拉起与失败回滚。
func (s *InstallService) selfUpdate(ctx context.Context, format, filePath string) error {
	return selfupdate.New().PrepareAndStart(ctx, format, filePath)
}
