package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"vakaohub/launcher/internal/api"
	"vakaohub/launcher/internal/install"
	"vakaohub/launcher/internal/store"
)

type InstallService struct {
	host *Host
}

func NewInstall(host *Host) *InstallService {
	return &InstallService{host: host}
}

// Install 下载、校验并安装指定产物。
func (s *InstallService) Install(appKey string, artifactID int) (store.Installed, error) {
	s.host.opMu.Lock()
	defer s.host.opMu.Unlock()

	platform, arch := platformInfo()
	client := s.host.newClient()
	response, err := client.Catalog(s.host.context(), platform, arch)
	if err != nil {
		return store.Installed{}, err
	}

	var app *api.CatalogApp
	var artifact *api.Artifact
	for i := range response.Apps {
		if response.Apps[i].AppKey != appKey || response.Apps[i].Latest == nil {
			continue
		}
		app = &response.Apps[i]
		for j := range app.Latest.Artifacts {
			if app.Latest.Artifacts[j].ID == artifactID {
				artifact = &app.Latest.Artifacts[j]
				break
			}
		}
		break
	}
	if app == nil || artifact == nil {
		return store.Installed{}, fmt.Errorf("未找到该应用或产物")
	}
	if artifact.Platform == "darwin" && artifact.MinOs != "" {
		if current := hostOSVersion(); current != "" && isNewerVersion(artifact.MinOs, current) {
			return store.Installed{}, fmt.Errorf("该版本需要 macOS %s 或更高版本，当前系统为 %s", artifact.MinOs, current)
		}
	}

	s.host.emit(appKey, "preparing", -1, "准备下载")

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
		return store.Installed{}, err
	}
	defer os.RemoveAll(tmpDir)

	fileName := artifact.FileName
	if fileName == "" {
		fileName = fmt.Sprintf("%s.%s", appKey, artifact.Format)
	}
	filePath := filepath.Join(tmpDir, filepath.Base(fileName))

	sha, err := s.download(appKey, artifact.DownloadURL, artifact.ID, filePath, artifact.Size)
	if err != nil {
		s.host.onceEvent(eventKey+":download_fail", withType(event, eventDownloadFail))
		s.host.emit(appKey, "error", -1, err.Error())
		return store.Installed{}, err
	}
	s.host.onceEvent(eventKey+":download_success", withType(event, eventDownloadSuccess))

	s.host.emit(appKey, "verifying", 100, "校验 SHA-256")
	if !strings.EqualFold(sha, artifact.SHA256) {
		s.host.onceEvent(eventKey+":verify_fail", withType(event, eventVerifyFail))
		message := fmt.Sprintf("校验失败：期望 %s，实际 %s", artifact.SHA256, sha)
		s.host.emit(appKey, "error", -1, message)
		return store.Installed{}, fmt.Errorf("%s", message)
	}

	// 启动器更新自身：运行中的 bundle 不能就地覆盖，交给后台脚本在本进程退出后替换。
	if appKey == "vakaohub" {
		s.host.emit(appKey, "installing", 100, "正在准备替换启动器")
		if err := s.selfUpdate(artifact.Format, filePath); err != nil {
			s.host.onceEvent(eventKey+":install_fail", withType(event, eventInstallFail))
			s.host.emit(appKey, "error", -1, err.Error())
			return store.Installed{}, err
		}
		s.host.onceEvent(eventKey+":install_success", withType(event, eventInstallSuccess))
		current, _, _ := install.CurrentBundle()
		s.host.emit(appKey, "done", 100, "更新已就绪，重启启动器后完成替换")
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

	s.host.emit(appKey, "installing", 100, "正在安装")
	installPath, err := install.Install(
		artifact.Format, filePath, app.AppKey, app.Name, existingPath, s.host.installDir(),
	)
	if err != nil {
		s.host.onceEvent(eventKey+":install_fail", withType(event, eventInstallFail))
		s.host.emit(appKey, "error", -1, err.Error())
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
			s.host.emit(appKey, "done", 100, "已启动安装程序，请按向导完成安装")
		} else {
			s.host.emit(appKey, "done", 100, "安装完成；未记录启动路径，请从系统应用列表启动")
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
		return store.Installed{}, err
	}

	s.host.emit(appKey, "done", 100, "安装完成")
	return item, nil
}

func (s *InstallService) Uninstall(appKey string, deleteData bool) error {
	s.host.opMu.Lock()
	defer s.host.opMu.Unlock()

	if appKey == "vakaohub" {
		return fmt.Errorf("不能卸载正在运行的启动器")
	}

	item, ok := s.host.store.Get(appKey)
	if !ok {
		return fmt.Errorf("尚未安装该应用")
	}

	bundleID := ""
	if deleteData {
		value, err := install.BundleID(item.InstallPath)
		if err != nil {
			return err
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
		return install.RemoveData(bundleID)
	}
	return nil
}

// SelectInstallDir 打开系统目录选择器；用户取消时返回空串。
// 选中即校验（存在、可写、在允许范围内），避免保存后才发现装不上。
func (s *InstallService) SelectInstallDir() (string, error) {
	if s.host.app == nil {
		return "", fmt.Errorf("当前环境不支持目录选择")
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
		return "", err
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

func (s *InstallService) download(appKey, url string, artifactID int, dest string, expectedSize int64) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	directory := filepath.Join(cache, "vakaohub", "downloads")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
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

	request, err := http.NewRequestWithContext(s.host.context(), http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	if start > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", start))
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
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
			return "", fmt.Errorf("下载失败：HTTP %d", response.StatusCode)
		}
	default:
		return "", fmt.Errorf("下载失败：HTTP %d", response.StatusCode)
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
			return "", err
		}

		total := expectedSize
		if total <= 0 && response.ContentLength > 0 {
			total = start + response.ContentLength
		}
		received := start
		lastPercent := -1
		lastEmit := time.Now()
		buffer := make([]byte, 128*1024)

		for {
			n, readErr := response.Body.Read(buffer)
			if n > 0 {
				if _, err := file.Write(buffer[:n]); err != nil {
					file.Close()
					return "", err
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
					s.host.emit(appKey, "downloading", percent, "下载中")
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				file.Close()
				return "", readErr
			}
		}
		if err := file.Close(); err != nil {
			return "", err
		}
	}

	if expectedSize > 0 {
		info, err := os.Stat(partPath)
		if err != nil {
			return "", err
		}
		if info.Size() != expectedSize {
			return "", fmt.Errorf("下载不完整：期望 %d 字节，实际 %d 字节", expectedSize, info.Size())
		}
	}

	s.host.emit(appKey, "downloading", 100, "下载完成")
	sha, err := fileSHA256(partPath)
	if err != nil {
		return "", err
	}
	if err := os.Rename(partPath, dest); err != nil {
		return "", err
	}
	return sha, nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
