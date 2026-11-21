package apperr

// kindMessage 是每一类错误的唯一中文文案来源。
func kindMessage(kind Kind) string {
	switch kind {
	case KindNetwork:
		return "无法连接更新服务，请检查网络后重试"
	case KindTimeout:
		return "操作超时，请重试"
	case KindCanceled:
		return "已取消"
	case KindBusy:
		return "应用正在运行，请退出后重试"
	case KindPermission:
		return "没有写入权限，请在设置中更换安装目录"
	case KindDisk:
		return "磁盘空间不足，请清理后重试"
	case KindCorrupt:
		return "安装包损坏或校验失败，请重试"
	case KindSignature:
		return "安装包签名校验失败，已拒绝安装"
	case KindUnsupported:
		return "该格式不支持自动安装，请手动安装"
	case KindServer:
		return "服务暂时不可用，请稍后重试"
	default:
		return "发生内部错误，请查看日志"
	}
}

// kindCode 是稳定的机器可读错误码，前端与日志都依赖它。
func kindCode(kind Kind) string {
	switch kind {
	case KindNetwork:
		return "E_NETWORK"
	case KindTimeout:
		return "E_TIMEOUT"
	case KindCanceled:
		return "E_CANCELED"
	case KindBusy:
		return "E_BUSY"
	case KindPermission:
		return "E_PERMISSION"
	case KindDisk:
		return "E_DISK"
	case KindCorrupt:
		return "E_CORRUPT"
	case KindSignature:
		return "E_SIGNATURE"
	case KindUnsupported:
		return "E_UNSUPPORTED"
	case KindServer:
		return "E_SERVER"
	default:
		return "E_INTERNAL"
	}
}

// kindRetryable 是默认可重试规则；server 的 4xx 分支会用 NewWithRetryable 覆盖。
func kindRetryable(kind Kind) bool {
	switch kind {
	case KindNetwork, KindTimeout, KindBusy, KindCorrupt, KindServer:
		return true
	default:
		return false
	}
}
