package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"vakaohub/launcher/internal/api"
	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/install"
	"vakaohub/launcher/internal/store"
)

// downloadClient 不设全局 Timeout：总时长由 context 控制，卡死由读循环的 stall 检测处理。
var downloadClient = &http.Client{}

type InstallService struct {
	host *Host
}

func NewInstall(host *Host) *InstallService {
	return &InstallService{host: host}
}

// Install 下载、校验并安装指定产物。
func (s *InstallService) Install(ctx context.Context, appKey string, artifactID int) (store.Installed, error) {
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
	if artifact.Platform == "darwin" && artifact.MinOs != "" {
		if current := hostOSVersion(); current != "" && isNewerVersion(artifact.MinOs, current) {
			err := apperr.New(
				apperr.KindUnsupported,
				"install.minos",
				fmt.Sprintf("该版本需要 macOS %s 或更高版本，当前系统为 %s", artifact.MinOs, current),
				nil,
			)
			s.fail(appKey, opID, err)
			return store.Installed{}, err
		}
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
	if appKey == "vakaohub" {
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
		if artifact.Format == "exe" {
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

	if appKey == "vakaohub" {
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

func (s *InstallService) download(ctx context.Context, opID, appKey, url string, artifactID int, dest string, expectedSize int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", apperr.FromContext(err, "download")
	}
	ctx, cancel := context.WithTimeout(ctx, s.host.timeouts.Download)
	defer cancel()

	cache, err := os.UserCacheDir()
	if err != nil {
		return "", apperr.FromOS(err, "download.cache")
	}
	directory := filepath.Join(cache, "vakaohub", "downloads")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", apperr.FromOS(err, "download.cache")
	}
	partPath := filepath.Join(directory, fmt.Sprintf("%d.part", artifactID))

	var start int64
	if info, err := os.Stat(partPath); err == nil {
		start = info.Size()
	}
	if expectedSize > 0 && start > expectedSize {
		start = 0
		_ = os.Remove(partPath)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", apperr.New(apperr.KindInternal, "download.request", "下载地址无效，请刷新后重试", err)
	}
	if start > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", start))
	}

	response, err := downloadClient.Do(request)
	if err != nil {
		return "", apperr.FromContext(err, "download")
	}
	defer response.Body.Close()

	complete := false
	switch response.StatusCode {
	case http.StatusPartialContent:
		// 续传：保留已有部分，追加写入
	case http.StatusOK:
		// 服务端不支持 Range 或文件已变化，从头开始
		start = 0
	case http.StatusRequestedRangeNotSatisfiable:
		if expectedSize > 0 && start == expectedSize {
			complete = true
		} else {
			return "", apperr.FromHTTPStatus(response.StatusCode, "download", "")
		}
	default:
		return "", apperr.FromHTTPStatus(response.StatusCode, "download", "")
	}

	if !complete {
		flags := os.O_CREATE | os.O_WRONLY
		if start > 0 {
			flags |= os.O_APPEND
		} else {
			flags |= os.O_TRUNC
		}
		file, err := os.OpenFile(partPath, flags, 0o644)
		if err != nil {
			return "", apperr.FromOS(err, "download")
		}

		total := expectedSize
		if total <= 0 && response.ContentLength > 0 {
			total = start + response.ContentLength
		}
		received := start
		lastPercent := -1
		lastEmit := time.Now()
		buffer := make([]byte, 128*1024)
		guard := newStallGuard(response.Body, s.host.timeouts.DownloadStall)
		defer guard.Stop()

		for {
			if err := ctx.Err(); err != nil {
				_ = file.Close()
				return "", apperr.FromContext(err, "download")
			}
			n, readErr := guard.Read(buffer)
			if n > 0 {
				if _, err := file.Write(buffer[:n]); err != nil {
					_ = file.Close()
					return "", apperr.FromOS(err, "download")
				}
				received += int64(n)
				percent := -1
				if total > 0 {
					percent = int(received * 100 / total)
					if percent > 100 {
						percent = 100
					}
				}
				if percent != lastPercent && time.Since(lastEmit) > 80*time.Millisecond {
					lastPercent = percent
					lastEmit = time.Now()
					s.host.emit(appKey, opID, "downloading", percent, "下载中", false)
				}
			}
			if readErr == io.EOF {
				guard.Stop()
				break
			}
			if readErr != nil {
				guard.Stop()
				_ = file.Close()
				if guard.TimedOut() {
					return "", apperr.New(apperr.KindTimeout, "download.stall", "", readErr)
				}
				return "", apperr.FromContext(readErr, "download")
			}
		}
		if err := file.Close(); err != nil {
			return "", apperr.FromOS(err, "download")
		}
	}

	if expectedSize > 0 {
		info, err := os.Stat(partPath)
		if err != nil {
			return "", apperr.FromOS(err, "download")
		}
		if info.Size() != expectedSize {
			return "", apperr.New(apperr.KindCorrupt, "download.size", "下载不完整，请重试", nil)
		}
	}

	s.host.emit(appKey, opID, "downloading", 100, "下载完成", false)
	sha, err := s.fileSHA256(ctx, partPath)
	if err != nil {
		return "", err
	}
	if err := os.Rename(partPath, dest); err != nil {
		return "", apperr.FromOS(err, "download")
	}
	return sha, nil
}

func (s *InstallService) fileSHA256(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, s.host.timeouts.Hash)
	defer cancel()

	file, err := os.Open(path)
	if err != nil {
		return "", apperr.FromOS(err, "verify")
	}
	defer file.Close()

	hash := sha256.New()
	buffer := make([]byte, 1024*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", apperr.FromContext(err, "verify")
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", apperr.FromOS(readErr, "verify")
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// stallGuard 在连续一段时间没有收到数据时关闭 body，把“连接还在但不动”转成超时。
type stallGuard struct {
	body     io.ReadCloser
	timeout  time.Duration
	timer    *time.Timer
	timedOut atomic.Bool
}

func newStallGuard(body io.ReadCloser, timeout time.Duration) *stallGuard {
	guard := &stallGuard{body: body, timeout: timeout}
	if timeout > 0 {
		guard.timer = time.AfterFunc(timeout, func() {
			guard.timedOut.Store(true)
			_ = body.Close()
		})
	}
	return guard
}

func (g *stallGuard) Read(p []byte) (int, error) {
	n, err := g.body.Read(p)
	if n > 0 && g.timer != nil {
		g.timer.Reset(g.timeout)
	}
	return n, err
}

func (g *stallGuard) Stop() {
	if g.timer != nil {
		g.timer.Stop()
	}
}

func (g *stallGuard) TimedOut() bool {
	return g.timedOut.Load()
}
