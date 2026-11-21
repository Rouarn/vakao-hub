//go:build !darwin && !windows

package selfupdate

import "errors"

func startDetached(_, _ string) error {
	return errors.New("当前平台暂不支持启动器自动更新")
}
