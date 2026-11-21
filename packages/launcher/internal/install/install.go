package install

import (
	"context"

	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/atomicfs"
)

// swapDirectory 是原子换入的注入点，测试可替换为失败实现验证旧版本不被破坏。
var swapDirectory = atomicfs.Swap

// Options 是一次安装所需的全部输入。
type Options struct {
	Format     string
	FilePath   string
	AppKey     string
	AppName    string
	TargetPath string // 非空表示升级已有安装，尽量保持原位置
	InstallDir string // 用户配置的安装根目录，空串表示平台默认
	Mode       string // 空串自动判断；uncertain 时由用户在便携/安装器之间选择
}

// Result 是安装结果：Path 为空表示安装器已启动、位置由它自己决定。
type Result struct {
	Path    string
	Kind    Kind
	Pending bool
}

// Install 按产物格式执行平台安装。
func Install(ctx context.Context, opts Options) (Result, error) {
	return installLocal(ctx, opts)
}

// Remove 删除已安装应用（仅允许平台约定的安装目录）。
// appKey 用于确认路径确实属于该应用的容器目录，避免误删同级其它内容。
func Remove(path, appKey string) error {
	if path == "" {
		return apperr.New(apperr.KindInternal, "uninstall", "没有记录安装路径，无法自动卸载", nil)
	}
	return removeLocal(path, appKey)
}

// Launch 启动已安装应用。
func Launch(path string) error {
	if path == "" {
		return apperr.New(apperr.KindInternal, "launch", "该安装方式未记录启动路径，请手动启动", nil)
	}
	return launchLocal(path)
}
