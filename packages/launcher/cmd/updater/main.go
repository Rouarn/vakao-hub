// Command updater 是自更新 helper 入口，只依赖 internal/updater、atomicfs、apperr。
package main

import (
	"fmt"
	"os"

	"vakaohub/launcher/internal/updater"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "用法: vakaohub-updater <job.json>")
		os.Exit(2)
	}

	job, err := updater.LoadJob(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取更新任务失败:", err)
		os.Exit(2)
	}

	result := updater.Run(job, updater.DefaultHooks())
	if err := updater.WriteResult(job.Result, result); err != nil {
		fmt.Fprintln(os.Stderr, "写入更新结果失败:", err)
		os.Exit(1)
	}
	if result.Status != updater.StatusSuccess {
		os.Exit(1)
	}
}
