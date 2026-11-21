// artifact-check 在发布前校验产物是否符合安装端支持的包形态。
// 与安装前的 install.ValidatePackage 共用同一份规则，避免“发布端放过、用户端报错”。
package main

import (
	"flag"
	"fmt"
	"os"

	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/install"
)

func main() {
	platform := flag.String("platform", "", "darwin 或 win32")
	format := flag.String("format", "", "zip / exe / msi")
	path := flag.String("path", "", "产物文件路径")
	flag.Parse()

	if *platform == "" || *format == "" || *path == "" {
		fmt.Fprintln(os.Stderr, "用法: artifact-check --platform win32 --format exe --path <文件>")
		os.Exit(2)
	}
	if err := install.ValidatePackage(*platform, *format, *path); err != nil {
		fmt.Fprintf(os.Stderr, "包形态检查失败：%s\n", apperr.UserMessage(err))
		os.Exit(1)
	}
	fmt.Printf("包形态检查通过：%s/%s\n", *platform, *format)
}
