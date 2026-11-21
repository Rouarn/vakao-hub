//go:build !darwin && !windows

package updater

// DefaultHooks 在暂不支持的平台返回空钩子，Run 会以“更新组件不完整”失败。
func DefaultHooks() Hooks {
	return Hooks{}
}
