// Package version 保存构建时注入的启动器版本号。
package version

// Version 由构建命令通过
// `-ldflags "-X vakaohub/launcher/internal/version.Version=<build/config.yml info.version>"`
// 注入；默认空串表示开发构建（未注入版本）。
var Version string
