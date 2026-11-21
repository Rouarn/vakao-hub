package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"vakaohub/launcher/internal/api"
	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/contract"
	"vakaohub/launcher/internal/install"
	"vakaohub/launcher/internal/store"
)

type InstallService struct {
	host *Host
}

func NewInstall(host *Host) *InstallService {
	return &InstallService{host: host}
}

// Install 下载、校验并安装指定产物；目标应用正在运行时返回可识别的 running 错误。
func (s *InstallService) Install(ctx context.Context, appKey string, artifactID int) (store.Installed, error) {
	return s.install(ctx, appKey, artifactID, false)
}

// QuitAppsAndInstall 在用户确认后先退出目标应用，再继续完成同一次安装。
func (s *InstallService) QuitAppsAndInstall(ctx context.Context, appKey string, artifactID int) (store.Installed, error) {
	return s.install(ctx, appKey, artifactID, true)
}

func (s *InstallService) install(ctx context.Context, appKey string, artifactID int, quitRunning bool) (store.Installed, error) {
	opID, release, err := s.host.beginOp()
	if err != nil {
		return store.Installed{}, err
	}
	defer release()

	platform, arch := platformInfo()
	raw, err := s.host.newClient().Catalog(ctx, platform, arch)
	if err != nil {
		return store.Installed{}, err
	}

	var app *api.CatalogApp
	var artifact *api.Artifact
	for i := range raw.Apps {
		if raw.Apps[i].AppKey != appKey || raw.Apps[i].Latest == nil {
			continue
		}
		app = &raw.Apps[i]
		for j := range app.Latest.Artifacts {
			if app.Latest.Artifacts[j].ID == artifactID {
				artifact = &app.Latest.Artifacts[j]
				break
			}
		}
		break
	}
	if app == nil || artifact == nil {
		resolved := apperr.New(apperr.KindInternal, "install.resolve", "未找到该应用或产物", nil)
		s.fail(appKey, opID, resolved)
		return store.Installed{}, resolved
	}
	if err := s.ensureMinOS(appKey, opID, artifact); err != nil {
		return store.Installed{}, err
	}

	s.host.emit(appKey, opID, "preparing", -1, "准备下载", false)

	// 一次安装对应一次下载动作：以下载/安装事件共用同一份上下文，
	// 去重键统一用产物 ID，重试或连点不会重复计数。
	installedCode := 0
	if record, ok := s.host.store.Get(appKey); ok {
		installedCode = record.VersionCode
	}
	event := s.host.eventFor(appKey, platform, arch)
	event.ReleaseID = app.Latest.ID
	event.ArtifactID = artifact.ID
	event.FromVersionCode = installedCode
	event.ToVersionCode = app.Latest.VersionCode
	eventKey := fmt.Sprintf("artifact:%d", artifact.ID)
	s.host.onceEvent(eventKey+":download_start", withType(event, eventDownloadStart))

	tmpDir, err := os.MkdirTemp("", "vakaohub-install-*")
	if err != nil {
		typed := apperr.FromOS(err, "install.temp")
		s.fail(appKey, opID, typed)
		return store.Installed{}, typed
	}
	defer os.RemoveAll(tmpDir)

	fileName := artifact.FileName
	if fileName == "" {
		fileName = fmt.Sprintf("%s.%s", appKey, artifact.Format)
	}
	filePath := filepath.Join(tmpDir, filepath.Base(fileName))

	sha, err := s.download(ctx, opID, appKey, artifact.DownloadURL, artifact.ID, filePath, artifact.Size)
	if err != nil {
		s.host.onceEvent(eventKey+":download_fail", withType(event, eventDownloadFail))
		s.fail(appKey, opID, err)
		return store.Installed{}, err
	}
	s.host.onceEvent(eventKey+":download_success", withType(event, eventDownloadSuccess))

	s.host.emit(appKey, opID, "verifying", 100, "校验 SHA-256", false)
	if !strings.EqualFold(sha, artifact.SHA256) {
		s.host.onceEvent(eventKey+":verify_fail", withType(event, eventVerifyFail))
		slog.Error("checksum mismatch", "app", appKey, "expected", artifact.SHA256, "actual", sha)
		err := apperr.New(apperr.KindCorrupt, "verify", "", nil)
		s.fail(appKey, opID, err)
		return store.Installed{}, err
	}

	// 启动器更新自身：运行中的 bundle 不能就地覆盖，交给后台脚本在本进程退出后替换。
	if appKey == selfAppKey {
		s.host.emit(appKey, opID, "installing", 100, "正在准备替换启动器", false)
		if err := s.selfUpdate(ctx, artifact.Format, filePath); err != nil {
			s.host.onceEvent(eventKey+":install_fail", withType(event, eventInstallFail))
			s.fail(appKey, opID, err)
			return store.Installed{}, err
		}
		s.host.onceEvent(eventKey+":install_success", withType(event, eventInstallSuccess))
		current, _, _ := install.CurrentBundle()
		s.host.emit(appKey, opID, "done", 100, "更新已就绪，重启启动器后完成替换", false)
		return store.Installed{
			AppKey:      appKey,
			Name:        app.Name,
			VersionName: app.Latest.VersionName,
			VersionCode: app.Latest.VersionCode,
			InstallPath: current,
			InstalledAt: time.Now().Unix(),
		}, nil
	}

	existingPath := ""
	if record, ok := s.host.store.Get(appKey); ok {
		if _, statErr := os.Stat(record.InstallPath); statErr == nil {
			existingPath = record.InstallPath
		}
	}
	if existingPath == "" {
		if found := install.FindApps(app.Name); len(found) > 0 {
			existingPath = found[0].Path
		}
	}

	// 安装前检查目标目录内是否有进程占用：首次命中只返回 running 错误，
	// 由前端确认后再走 QuitAppsAndInstall 继续，不在未确认时结束任何进程。
	if existingPath != "" {
		processes := s.checkRunning(appKey, opID, app.Name, existingPath)
		if len(processes) > 0 {
			if !quitRunning {
				blocked := apperr.New(
					apperr.KindRunning,
					"install.running",
					fmt.Sprintf("应用「%s」正在运行（%d 个进程），需要先退出才能更新", app.Name, len(processes)),
					nil,
				)
				s.fail(appKey, opID, blocked)
				return store.Installed{}, blocked
			}
			err := install.QuitProcesses(processes, install.QuitOptions{
				Grace: install.QuitGrace,
				Report: func(message string) {
					s.host.emit(appKey, opID, "quitting", -1, message, false)
				},
			})
			if err != nil {
				s.fail(appKey, opID, err)
				return store.Installed{}, err
			}
		}
	}

	s.host.emit(appKey, opID, "installing", 100, "正在安装", false)
	installPath, err := install.Install(
		ctx, artifact.Format, filePath, app.AppKey, app.Name, existingPath, s.host.installDir(),
	)
	if err != nil {
		s.host.onceEvent(eventKey+":install_fail", withType(event, eventInstallFail))
		s.fail(appKey, opID, err)
		return store.Installed{}, err
	}
	// msi / exe 拿不到安装结果，Install 返回 nil 只代表安装程序已正常启动，事件按成功计。
	s.host.onceEvent(eventKey+":install_success", withType(event, eventInstallSuccess))
	// 安装后让已安装索引重新扫描，下一次刷新能看到新条目。
	install.InvalidateIndex()

	if installPath == "" {
		// msi / exe 交给系统安装程序，拿不到可启动路径；不写空路径记录，
		// 否则下次刷新时 os.Stat("") 必然失败并删掉记录，表现为“装完仍显示未安装”。
		if artifact.Format == contract.FormatEXE {
			s.host.emit(appKey, opID, "done", 100, "已启动安装程序，请按向导完成安装", false)
		} else {
			s.host.emit(appKey, opID, "done", 100, "安装完成；未记录启动路径，请从系统应用列表启动", false)
		}
		return store.Installed{}, nil
	}

	item := store.Installed{
		AppKey:      app.AppKey,
		Name:        app.Name,
		VersionName: app.Latest.VersionName,
		VersionCode: app.Latest.VersionCode,
		InstallPath: installPath,
		InstalledAt: time.Now().Unix(),
	}
	if err := s.host.store.Put(item); err != nil {
		typed := apperr.New(apperr.KindInternal, "install.store", "保存安装记录失败，请重试", err)
		s.fail(appKey, opID, typed)
		return store.Installed{}, typed
	}

	s.host.emit(appKey, opID, "done", 100, "安装完成", false)
	return item, nil
}

// selfAppKey 是启动器自身；它走自更新 helper，不参与「运行中应用」检测。
const selfAppKey = "vakaohub"

// detectRunning 只在安装普通应用时检查目标目录；自更新路径必须完全绕过，
// 否则会误判启动器自己正在运行而拦截 helper 流程。
func (s *InstallService) detectRunning(appKey, target string) ([]install.RunningProcess, error) {
	if appKey == selfAppKey {
		return nil, nil
	}
	return runningProcesses(target)
}

// runningProcesses 允许测试模拟检测失败。
var runningProcesses = install.RunningProcesses

// checkRunning 在安装前检测目标目录内的进程。无法检测时不阻断安装，
// 但必须让用户知道「运行状态未能确认、已直接继续」，不能静默覆盖。
func (s *InstallService) checkRunning(appKey, opID, appName, target string) []install.RunningProcess {
	processes, err := s.detectRunning(appKey, target)
	if err != nil {
		slog.Warn("检测运行中应用失败", "app", appKey, "error", err)
		s.host.emit(appKey, opID, "warning", -1, runningUnknownMessage(appName), false)
		return nil
	}
	return processes
}

func runningUnknownMessage(appName string) string {
	return fmt.Sprintf("无法确认「%s」是否正在运行，已直接继续安装；如安装失败，请先手动退出该应用", appName)
}

// fail 统一把错误写日志并推送给前端：日志保留系统原话，UI 只拿中文文案。
func (s *InstallService) fail(appKey, opID string, err error) {
	slog.Error(
		"install failed",
		"app", appKey,
		"kind", apperr.KindOf(err),
		"op", apperr.Operation(err),
		"cause", err,
	)
	s.host.emit(appKey, opID, "error", -1, apperr.UserMessage(err), apperr.IsRetryable(err))
}

func (s *InstallService) Uninstall(appKey string, deleteData bool) error {
	_, release, err := s.host.beginOp()
	if err != nil {
		return err
	}
	defer release()

	if appKey == selfAppKey {
		return apperr.New(apperr.KindInternal, "uninstall", "不能卸载正在运行的启动器", nil)
	}

	item, ok := s.host.store.Get(appKey)
	if !ok {
		return apperr.New(apperr.KindInternal, "uninstall", "尚未安装该应用", nil)
	}

	bundleID := ""
	if deleteData {
		value, err := install.BundleID(item.InstallPath)
		if err != nil {
			return apperr.New(apperr.KindInternal, "uninstall", "读取应用信息失败，请重试", err)
		}
		bundleID = value
	}

	if err := install.Remove(item.InstallPath); err != nil {
		return err
	}
	_ = s.host.store.Remove(appKey)
	// 卸载后让已安装索引重新扫描，避免残留条目继续显示为已安装。
	install.InvalidateIndex()

	if deleteData {
		if err := install.RemoveData(bundleID); err != nil {
			return apperr.FromOS(err, "uninstall.data")
		}
	}
	return nil
}

// SelectInstallDir 打开系统目录选择器；用户取消时返回空串。
// 选中即校验（存在、可写、在允许范围内），避免保存后才发现装不上。
func (s *InstallService) SelectInstallDir() (string, error) {
	_, release, err := s.host.beginOp()
	if err != nil {
		return "", err
	}
	defer release()

	if s.host.app == nil {
		return "", apperr.New(apperr.KindInternal, "select-dir", "当前环境不支持目录选择", nil)
	}
	defaultDir := install.ExpandDir(s.host.installDir())
	if defaultDir == "" {
		defaultDir = install.DefaultInstallDir()
	}
	dialog := s.host.app.Dialog.OpenFile()
	dialog.SetTitle("选择应用安装目录")
	dialog.SetDirectory(defaultDir)
	dialog.CanChooseDirectories(true)
	dialog.CanChooseFiles(false)
	dir, err := dialog.PromptForSingleSelection()
	if err != nil {
		return "", apperr.FromContext(err, "select-dir")
	}
	if dir == "" {
		return "", nil
	}
	if err := install.ValidateInstallDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// GetDefaultInstallDir 返回平台默认安装目录，供前端「恢复默认」展示。
func (s *InstallService) GetDefaultInstallDir() string {
	return install.DefaultInstallDir()
}
