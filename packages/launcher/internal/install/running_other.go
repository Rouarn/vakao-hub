//go:build !darwin && !windows

package install

import "errors"

// 其它平台暂不提供运行中检测与退出能力，安装流程保持原样。
func listRunning(string) ([]RunningProcess, error) {
	return nil, nil
}

func requestQuit([]RunningProcess) error {
	return nil
}

func processAlive(int) bool {
	return false
}

func forceKill(int) error {
	return errors.New("当前平台不支持结束进程")
}
