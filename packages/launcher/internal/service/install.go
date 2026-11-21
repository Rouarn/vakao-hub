package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"vakaohub/launcher/internal/api"
	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/contract"
	"vakaohub/launcher/internal/install"
	"vakaohub/launcher/internal/store"
)

type InstallService struct {
	host *Host

	mu      sync.Mutex
	pending *pendingArtifact
}

// pendingArtifact 是分类不确定时保留的已下载产物：
// 用户在便携/安装器之间选择后继续安装，避免重新下载整个包。
type pendingArtifact struct {
	appKey     string
	artifactID int
	format     string
	path       string
}

func NewInstall(host *Host) *InstallService {
	return &InstallService{host: host}
}

// Install 下载、校验并安装指定产物；目标应用正在运行时返回可识别的 running 错误。
func (s *InstallService) Install(ctx context.Context, appKey string, artifactID int) (store.Installed, error) {
	return s.install(ctx, appKey, artifactID, false, install.ModeAuto)
}

// InstallWithMode 在分类不确定、用户选择安装方式后继续同一次安装。
func (s *InstallService) InstallWithMode(
	ctx context.Context, appKey string, artifactID int, mode string,
) (store.Installed, error) {
	if mode != install.ModePortable && mode != install.ModeInstaller {
		return store.Installed{}, apperr.New(apperr.KindInternal, "install.mode", "未知的安装方式", nil)
	}
	return s.install(ctx, appKey, artifactID, false, mode)
}

// QuitAppsAndInstall 在用户确认后先退出目标应用，再继续完成同一次安装。
func (s *InstallService) QuitAppsAndInstall(ctx context.Context, appKey string, artifactID int) (store.Installed, error) {
	return s.install(ctx, appKey, artifactID, true, install.ModeAuto)
}

func (s *InstallService) install(
	ctx context.Context, appKey string, artifactID int, quitRunning bool, mode string,
) (store.Installed, error) {
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

	// 分类不确定时会把已下载的产物保留下来；用户选择安装方式后从这里续上，不重复下载。
	filePath := s.resumePending(appKey, artifact.ID)
	if filePath != "" {
		defer os.Remove(filePath)
	} else {
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
		filePath = filepath.Join(tmpDir, filepath.Base(fileName))

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
	}

	// 安装前预检包结构：形态不符合约定时直接给出具体原因，不再走到解压/调起安装器才失败。
	if err := install.ValidatePackage(platform, artifact.Format, filePath); err != nil {
		s.host.onceEvent(eventKey+":verify_fail", withType(event, eventVerifyFail))
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

	// 裸 exe 需要先判断是便携应用还是安装向导。判不准时不擅自决定：
	// 保留已下载的产物，让用户二选一，再继续同一次安装。
	if artifact.Format == contract.FormatEXE && mode == install.ModeAuto {
		classification := install.Classify(artifact.Format, filePath)
		if classification.Confidence == install.ConfidenceUncertain {
			preserved, preserveErr := preserveForChoice(artifact.ID, artifact.Format, filePath)
			if preserveErr != nil {
				typed := apperr.FromOS(preserveErr, "install.choice")
				s.fail(appKey, opID, typed)
				return store.Installed{}, typed
			}
			s.rememberPending(appKey, artifact.ID, artifact.Format, preserved)
			target := install.PreviewTargetDir(app.AppKey, existingPath, s.host.installDir())
			message := fmt.Sprintf(
				"未识别到安装程序标记。将作为便携应用复制到 %s；如果它其实是安装程序，请选择「交给安装程序」。",
				target,
			)
			return store.Installed{}, apperr.New(apperr.KindChoice, "install.choice", message, nil)
		}
		if classification.Kind == install.KindInstaller {
			mode = install.ModeInstaller
		} else {
			mode = install.ModePortable
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

	message := "正在安装"
	if mode != install.ModeInstaller && artifact.Format != contract.FormatMSI {
		if target := install.PreviewTargetDir(app.AppKey, existingPath, s.host.installDir()); target != "" {
			message = fmt.Sprintf("正在安装到 %s", target)
		}
	}
	s.host.emit(appKey, opID, "installing", 100, message, false)
	result, err := install.Install(ctx, install.Options{
		Format:     artifact.Format,
		FilePath:   filePath,
		AppKey:     app.AppKey,
		AppName:    app.Name,
		TargetPath: existingPath,
		InstallDir: s.host.installDir(),
		Mode:       mode,
	})
	if err != nil {
		s.host.onceEvent(eventKey+":install_fail", withType(event, eventInstallFail))
		s.fail(appKey, opID, err)
		return store.Installed{}, err
	}
	// msi / exe 安装器只保证安装程序已正常启动或已静默结束，事件按成功计。
	s.host.onceEvent(eventKey+":install_success", withType(event, eventInstallSuccess))
	// 安装后让已安装索引重新扫描，下一次刷新能看到新条目。
	install.InvalidateIndex()

	item := store.Installed{
		AppKey:      app.AppKey,
		Name:        app.Name,
		VersionName: app.Latest.VersionName,
		VersionCode: app.Latest.VersionCode,
		InstallPath: result.Path,
		InstalledAt: time.Now().Unix(),
		Kind:        string(result.Kind),
		Pending:     result.Pending,
	}
	if result.Kind == install.KindInstaller {
		// 安装器类先记下「已安装待确认」，目录可能为空；catalog 会以卸载注册表为准补全或清理。
		if err := s.host.store.Put(item); err != nil {
			typed := apperr.New(apperr.KindInternal, "install.store", "保存安装记录失败，请重试", err)
			s.fail(appKey, opID, typed)
			return store.Installed{}, typed
		}
		if result.Path == "" {
			s.host.emit(appKey, opID, "done", 100, "已启动安装程序，安装位置由它自己决定；完成后刷新即可", false)
		} else {
			s.host.emit(appKey, opID, "done", 100, "安装完成", false)
		}
		return item, nil
	}

	if result.Path == "" {
		typed := apperr.New(apperr.KindInternal, "install.path", "安装完成但未拿到启动路径，请刷新后重试", nil)
		s.fail(appKey, opID, typed)
		return store.Installed{}, typed
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

// preserveForChoice 把已下载的产物复制到临时待定目录：
// 用户选择安装方式后继续安装，不必重新下载；系统临时目录由操作系统自行回收。
func preserveForChoice(artifactID int, format, source string) (string, error) {
	dir := filepath.Join(os.TempDir(), "vakaohub-pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	target := filepath.Join(dir, fmt.Sprintf("%d.%s", artifactID, format))

	input, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer input.Close()

	output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return "", err
	}
	if err := output.Close(); err != nil {
		return "", err
	}
	return target, nil
}

func (s *InstallService) rememberPending(appKey string, artifactID int, format, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = &pendingArtifact{appKey: appKey, artifactID: artifactID, format: format, path: path}
}

// resumePending 只在应用与产物都匹配时复用保留的包，避免误用上一个应用的下载。
func (s *InstallService) resumePending(appKey string, artifactID int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := s.pending
	if pending == nil || pending.artifactID != artifactID || pending.appKey != appKey {
		return ""
	}
	s.pending = nil
	if _, err := os.Stat(pending.path); err != nil {
		return ""
	}
	return pending.path
}

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

	if item.Kind == string(install.KindInstaller) {
		// 安装器类的文件位置与卸载方式都由它自己决定，只能调起注册表里的卸载程序。
		if err := install.UninstallInstaller(item.Name); err != nil {
			return err
		}
		// 用户可能在卸载向导里取消；保留 pending 记录，等注册表项真正消失后由 catalog 清理。
		item.Pending = true
		item.InstalledAt = time.Now().Unix()
		_ = s.host.store.Put(item)
		install.InvalidateIndex()
		return nil
	}

	bundleID := ""
	if deleteData {
		value, err := install.BundleID(item.InstallPath)
		if err != nil {
			return apperr.New(apperr.KindInternal, "uninstall", "读取应用信息失败，请重试", err)
		}
		bundleID = value
	}

	if err := install.Remove(item.InstallPath, item.AppKey); err != nil {
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

// BindInstalledPath 让用户手动指定已安装应用的主程序。
// 用于安装器写注册表的名字对不上、或绿色软件放在别处导致无法自动识别的情况。
func (s *InstallService) BindInstalledPath(appKey, appName string) (store.Installed, error) {
	if appKey == selfAppKey {
		return store.Installed{}, apperr.New(apperr.KindInternal, "bind-path", "启动器自身不需要手动指定位置", nil)
	}
	_, release, err := s.host.beginOp()
	if err != nil {
		return store.Installed{}, err
	}
	defer release()

	if s.host.app == nil {
		return store.Installed{}, apperr.New(apperr.KindInternal, "bind-path", "当前环境不支持文件选择", nil)
	}
	dialog := s.host.app.Dialog.OpenFile()
	dialog.SetTitle("选择应用主程序")
	if dir := install.PreviewTargetDir(appKey, "", s.host.installDir()); dir != "" {
		dialog.SetDirectory(dir)
	}
	dialog.CanChooseFiles(true)
	dialog.CanChooseDirectories(false)
	path, err := dialog.PromptForSingleSelection()
	if err != nil {
		return store.Installed{}, apperr.FromContext(err, "bind-path")
	}
	if path == "" {
		return store.Installed{}, apperr.New(apperr.KindCanceled, "bind-path", "", nil)
	}
	if err := install.ValidatePackage(contract.PlatformWindows, contract.FormatEXE, path); err != nil {
		return store.Installed{}, err
	}

	item, ok := s.host.store.Get(appKey)
	if !ok {
		item = store.Installed{AppKey: appKey, Name: appName}
	}
	if item.Name == "" {
		item.Name = appName
	}
	// 手动指定说明用户确认它已经装好；按安装器类记录，卸载仍走系统卸载串。
	item.InstallPath = path
	item.Kind = string(install.KindInstaller)
	item.Pending = false
	item.InstalledAt = time.Now().Unix()
	if err := s.host.store.Put(item); err != nil {
		return store.Installed{}, apperr.New(apperr.KindInternal, "bind-path", "保存安装记录失败，请重试", err)
	}
	install.InvalidateIndex()
	return item, nil
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
