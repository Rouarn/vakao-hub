package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"vakaohub/launcher/internal/api"
	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/install"
	"vakaohub/launcher/internal/store"
)

type timeouts struct {
	Catalog       time.Duration
	Download      time.Duration
	DownloadStall time.Duration
	Hash          time.Duration
	Extract       time.Duration
	Installer     time.Duration
	SelfUpdate    time.Duration
}

func defaultTimeouts() timeouts {
	return timeouts{
		Catalog:       30 * time.Second,
		Download:      30 * time.Minute,
		DownloadStall: 60 * time.Second,
		Hash:          2 * time.Minute,
		Extract:       10 * time.Minute,
		Installer:     30 * time.Minute,
		SelfUpdate:    10 * time.Minute,
	}
}

// Host 持有 4 个 service 共享的依赖与状态。
type Host struct {
	app      *application.App
	store    *store.Store
	settings *SettingsState
	events   *EventQueue
	// opMu 保护 appOps 与 dialogOp：安装按应用加锁，系统选择器全局串行。
	opMu     sync.Mutex
	appOps   map[string]struct{}
	dialogOp bool
	timeouts timeouts
}

// NewHost 在 application.New 之后构造；app 只用于事件、窗口、对话框与退出。
func NewHost(app *application.App) *Host {
	st, err := store.Open()
	if err != nil {
		fmt.Println("读取已安装列表失败，本次运行使用内存记录：", err)
		st = store.NewMemory()
	}
	return newHost(app, st)
}

func newHost(app *application.App, st *store.Store) *Host {
	settings := loadSettings()
	// 扫描已安装应用时要一并检查用户配置的安装目录。
	install.SetInstallDir(settings.InstallDir)
	// 启动时清理超过 7 天的暂存/备份残留，不影响正常安装。
	install.CleanupStale()
	if settings.DeviceID == "" {
		settings.DeviceID = newDeviceID()
		// 写盘失败只会导致下次启动重新生成标识，不影响启动与上报。
		_ = saveSettings(settings)
	}

	host := &Host{
		app:      app,
		store:    st,
		settings: NewSettingsState(settings),
		events:   NewEventQueue(32),
		timeouts: defaultTimeouts(),
	}
	go host.eventWorker()
	return host
}

func (h *Host) context() context.Context {
	if h.app != nil {
		return h.app.Context()
	}
	return context.Background()
}

func (h *Host) newClient() *api.Client {
	return api.New(h.settings.BaseURL())
}

func (h *Host) installDir() string {
	return h.settings.Get().InstallDir
}

// beginAppOp 只对同一应用串行：不同应用可以同时下载、校验与安装，
// 同一个应用重复点击仍然只跑一次，避免两份安装互相覆盖。
func (h *Host) beginAppOp(appKey string) (string, func(), error) {
	h.opMu.Lock()
	if h.appOps == nil {
		h.appOps = make(map[string]struct{})
	}
	if _, busy := h.appOps[appKey]; busy {
		h.opMu.Unlock()
		return "", nil, apperr.New(apperr.KindBusy, "operation.app", "该应用正在更新，请等当前任务完成", nil)
	}
	h.appOps[appKey] = struct{}{}
	h.opMu.Unlock()

	release := func() {
		h.opMu.Lock()
		delete(h.appOps, appKey)
		h.opMu.Unlock()
	}
	return newOperationID(), release, nil
}

// beginDialog 串行化系统选择器：同时弹出两个模态会让窗口层级与焦点不可控。
func (h *Host) beginDialog() (func(), error) {
	h.opMu.Lock()
	defer h.opMu.Unlock()
	if h.dialogOp {
		return nil, apperr.New(apperr.KindBusy, "dialog", "已有一个选择窗口在等待，请先完成它", nil)
	}
	h.dialogOp = true
	return func() {
		h.opMu.Lock()
		h.dialogOp = false
		h.opMu.Unlock()
	}, nil
}

func newOperationID() string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("op-%d", time.Now().UnixNano())
	}
	return "op-" + hex.EncodeToString(buffer)
}

// emitProgress 是事件出口，测试可替换；app 为空时（单元测试）默认丢弃。
var emitProgress = func(app *application.App, payload Progress) {
	if app == nil {
		return
	}
	app.Event.Emit("install:progress", payload)
}

func (h *Host) emit(appKey, opID, phase string, percent int, message string, retryable bool) {
	emitProgress(h.app, Progress{
		AppKey:      appKey,
		Phase:       phase,
		Percent:     percent,
		Message:     message,
		OperationID: opID,
		Retryable:   retryable,
	})
}
