package main

import (
	"context"
	"time"

	"vakaohub/launcher/internal/api"
)

// 客户端事件类型，必须与服务端 EVENT_TYPES 保持一致，否则会被服务端静默丢弃。
const (
	eventCheck           = "check"
	eventDownloadStart   = "download_start"
	eventDownloadSuccess = "download_success"
	eventDownloadFail    = "download_fail"
	eventVerifyFail      = "verify_fail"
	eventInstallSuccess  = "install_success"
	eventInstallFail     = "install_fail"
	eventLaunch          = "launch"
)

const eventReportTimeout = 5 * time.Second

// reportEvent 只负责把事件投进队列：队列满说明上报速度已经跟不上用户操作，
// 此时丢弃事件比阻塞检查更新、下载或安装更合理。
func (a *App) reportEvent(event api.EventReport) {
	select {
	case a.events <- event:
	default:
	}
}

// onceEvent 按 key 在进程内去重：一次下载/安装动作即使被重试或重复点击，
// 也只对应一条事件，避免把统计放大。
func (a *App) onceEvent(key string, event api.EventReport) {
	a.sentMu.Lock()
	if a.sent[key] {
		a.sentMu.Unlock()
		return
	}
	a.sent[key] = true
	a.sentMu.Unlock()
	a.reportEvent(event)
}

// eventWorker 串行消费事件队列：顺序上报保证 download_success 先于 install_success，
// 失败直接丢弃，不上抛、不重试。
func (a *App) eventWorker() {
	for event := range a.events {
		ctx, cancel := context.WithTimeout(context.Background(), eventReportTimeout)
		_ = api.New(a.baseURL()).ReportEvent(ctx, event)
		cancel()
	}
}

func (a *App) deviceID() string {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.settings.DeviceID
}

// eventFor 填充所有事件共有的应用与设备上下文，调用方再补类型与版本字段。
func (a *App) eventFor(appKey, platform, arch string) api.EventReport {
	return api.EventReport{
		AppKey:   appKey,
		Platform: platform,
		Arch:     arch,
		DeviceID: a.deviceID(),
	}
}

func withType(event api.EventReport, eventType string) api.EventReport {
	event.Type = eventType
	return event
}
