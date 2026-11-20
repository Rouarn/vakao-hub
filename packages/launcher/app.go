package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"vakaohub/launcher/internal/api"
	"vakaohub/launcher/internal/contract"
	"vakaohub/launcher/internal/install"
	"vakaohub/launcher/internal/store"
	launcherversion "vakaohub/launcher/internal/version"
)

// App 是暴露给前端的方法集合。
type App struct {
	ctx        context.Context
	mu         sync.Mutex
	settingsMu sync.RWMutex
	settings   Settings
	store      *store.Store
	events     chan api.EventReport
	sentMu     sync.Mutex
	sent       map[string]bool
}

func NewApp() *App {
	s, err := store.Open()
	if err != nil {
		fmt.Println("读取已安装列表失败，本次运行使用内存记录：", err)
		s = store.NewMemory()
	}

	settings := loadSettings()
	// 扫描已安装应用时要一并检查用户配置的安装目录。
	install.SetInstallDir(settings.InstallDir)
	if settings.DeviceID == "" {
		settings.DeviceID = newDeviceID()
		// 写盘失败只会导致下次启动重新生成标识，不影响启动与上报。
		_ = saveSettings(settings)
	}

	app := &App{
		settings: settings,
		store:    s,
		events:   make(chan api.EventReport, 32),
		sent:     make(map[string]bool),
	}
	go app.eventWorker()
	return app
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// GetAppVersion 返回构建时注入的启动器版本（开发构建为空串）。
func (a *App) GetAppVersion() string {
	return launcherversion.Version
}

// GetDefaultBaseURL 返回内置的默认服务地址，前端「恢复默认」不再自带副本。
func (a *App) GetDefaultBaseURL() string {
	return defaultBaseURL
}

func (a *App) httpContext() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

// Progress 通过 Wails 事件推送给前端。
type Progress struct {
	AppKey  string `json:"appKey"`
	Phase   string `json:"phase"`
	Percent int    `json:"percent"`
	Message string `json:"message"`
}

type AppView struct {
	AppKey               string        `json:"appKey"`
	Name                 string        `json:"name"`
	Description          string        `json:"description"`
	IconURL              string        `json:"iconUrl"`
	Category             string        `json:"category"`
	Homepage             string        `json:"homepage"`
	RepoURL              string        `json:"repoUrl"`
	DocsURL              string        `json:"docsUrl"`
	ReleaseURL           string        `json:"releaseUrl"`
	ThemeColor           string        `json:"themeColor"`
	Tagline              string        `json:"tagline"`
	Features             []api.Feature `json:"features"`
	Screenshots          []string      `json:"screenshots"`
	LatestVersion        string        `json:"latestVersion"`
	LatestVersionCode    int           `json:"latestVersionCode"`
	LatestPublishedAt    int64         `json:"latestPublishedAt"`
	ForceUpdate          bool          `json:"forceUpdate"`
	UpdateLog            string        `json:"updateLog"`
	ArtifactID           int           `json:"artifactID"`
	ArtifactFormat       string        `json:"artifactFormat"`
	ArtifactSize         int64         `json:"artifactSize"`
	ArtifactSHA256       string        `json:"artifactSha256"`
	MinOs                string        `json:"minOs"`
	Installed            bool          `json:"installed"`
	InstalledVersion     string        `json:"installedVersion"`
	InstalledVersionCode int           `json:"installedVersionCode"`
	HasUpdate            bool          `json:"hasUpdate"`
	Running              bool          `json:"running"`
	InstallPath          string        `json:"installPath"`
	Duplicates           []string      `json:"duplicates"`
}

type CatalogView struct {
	Platform  string    `json:"platform"`
	Arch      string    `json:"arch"`
	OSVersion string    `json:"osVersion"`
	Apps      []AppView `json:"apps"`
}

func (a *App) GetSettings() Settings {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.settings
}

// baseURL 返回当前服务地址的副本，供网络调用在并发保存设置时安全读取。
func (a *App) baseURL() string {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.settings.BaseURL
}

func (a *App) SaveSettings(baseURL, installDir string) (Settings, error) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()

	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return a.settings, fmt.Errorf("服务地址不能为空")
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return a.settings, fmt.Errorf("服务地址必须以 http:// 或 https:// 开头")
	}
	installDir = strings.TrimSpace(installDir)
	if err := install.ValidateInstallDir(installDir); err != nil {
		return a.settings, err
	}
	next := a.settings
	next.BaseURL = baseURL
	next.InstallDir = installDir
	if err := saveSettings(next); err != nil {
		return a.settings, err
	}
	a.settings = next
	// 目录变化后已安装扫描范围也要跟着变，否则新装的应用刷新后不可见。
	install.SetInstallDir(installDir)
	return a.settings, nil
}

// GetDefaultInstallDir 返回平台默认安装目录，供前端「恢复默认」展示。
func (a *App) GetDefaultInstallDir() string {
	return install.DefaultInstallDir()
}

// SelectInstallDir 打开系统目录选择器；用户取消时返回空串。
// 选中即校验（存在、可写、在允许范围内），避免保存后才发现装不上。
func (a *App) SelectInstallDir() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("当前环境不支持目录选择")
	}
	defaultDir := install.ExpandDir(a.installDir())
	if defaultDir == "" {
		defaultDir = install.DefaultInstallDir()
	}
	dir, err := wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{
		Title:            "选择应用安装目录",
		DefaultDirectory: defaultDir,
	})
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

// installDir 返回当前配置的安装目录（可能为空，表示使用平台默认）。
func (a *App) installDir() string {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.settings.InstallDir
}

func (a *App) GetCatalog() (CatalogView, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.catalogLocked()
}

func (a *App) catalogLocked() (CatalogView, error) {
	platform, arch := platformInfo()
	client := api.New(a.baseURL())
	response, err := client.Catalog(a.httpContext(), platform, arch)
	if err != nil {
		return CatalogView{}, err
	}

	view := CatalogView{
		Platform:  platform,
		Arch:      arch,
		OSVersion: hostOSVersion(),
		Apps:      make([]AppView, 0, len(response.Apps)),
	}
	for _, app := range response.Apps {
		if app.Latest == nil || len(app.Latest.Artifacts) == 0 {
			continue
		}
		artifact := pickArtifact(app.Latest.Artifacts, platform, arch)
		if artifact == nil {
			continue
		}

		item := AppView{
			AppKey:            app.AppKey,
			Name:              app.Name,
			Description:       app.Description,
			IconURL:           app.IconURL,
			Category:          app.Category,
			Homepage:          app.Homepage,
			RepoURL:           app.RepoURL,
			DocsURL:           app.DocsURL,
			ReleaseURL:        app.ReleaseURL,
			ThemeColor:        app.ThemeColor,
			Tagline:           app.Tagline,
			Features:          app.Features,
			Screenshots:       app.Screenshots,
			LatestVersion:     app.Latest.VersionName,
			LatestVersionCode: app.Latest.VersionCode,
			LatestPublishedAt: app.Latest.PublishedAt,
			ForceUpdate:       app.Latest.ForceUpdate,
			UpdateLog:         app.Latest.UpdateLog,
			ArtifactID:        artifact.ID,
			ArtifactFormat:    artifact.Format,
			ArtifactSize:      artifact.Size,
			ArtifactSHA256:    artifact.SHA256,
			MinOs:             artifact.MinOs,
		}

		// 每次刷新都会重查平台已安装列表（Windows 走卸载注册表）：
		// MSI/EXE 向导安装完成后，注册表出现匹配条目即在此补记路径与版本，自动显示为已安装。
		detected := install.FindApps(app.Name)
		installedPath := ""
		installedVersion := ""
		installedCode := 0

		if app.AppKey == "vakaohub" {
			if path, version, ok := install.CurrentBundle(); ok {
				item.Installed = true
				item.Running = true
				item.InstallPath = path
				installedPath = path
				installedVersion = version
				// 用户可能把 bundle 拖到别的目录，安装记录要跟着实际位置走，
				// 否则后续任何按记录路径的操作都会指向已经不存在的旧位置。
				if record, exists := a.store.Get(app.AppKey); !exists ||
					record.InstallPath != path || record.VersionName != version {
					_ = a.store.Put(store.Installed{
						AppKey:      app.AppKey,
						Name:        app.Name,
						VersionName: version,
						InstallPath: path,
						InstalledAt: time.Now().Unix(),
					})
				}
			}
		}

		if !item.Installed {
			if record, ok := a.store.Get(app.AppKey); ok {
				if _, statErr := os.Stat(record.InstallPath); statErr == nil {
					item.Installed = true
					item.InstallPath = record.InstallPath
					installedPath = record.InstallPath
					installedVersion = record.VersionName
					installedCode = record.VersionCode
				} else {
					_ = a.store.Remove(app.AppKey)
				}
			}
		}

		if !item.Installed && len(detected) > 0 {
			item.Installed = true
			item.InstallPath = detected[0].Path
			installedPath = detected[0].Path
			installedVersion = detected[0].Version
			_ = a.store.Put(store.Installed{
				AppKey:      app.AppKey,
				Name:        app.Name,
				VersionName: installedVersion,
				VersionCode: 0,
				InstallPath: detected[0].Path,
				InstalledAt: time.Now().Unix(),
			})
		}

		if item.Installed {
			item.InstalledVersion = installedVersion
			item.InstalledVersionCode = installedCode
			if installedCode > 0 {
				item.HasUpdate = installedCode < app.Latest.VersionCode
			} else {
				item.HasUpdate = isNewerVersion(app.Latest.VersionName, installedVersion)
			}
		}

		for _, found := range detected {
			if found.Path != installedPath {
				item.Duplicates = append(item.Duplicates, found.Path)
			}
		}

		// 只有真的看到可更新版本才记一次 check：自动刷新与手动刷新都会走到这里，
		// 按「应用 + 目标版本」去重后同一次更新机会只计一次。
		if item.HasUpdate {
			event := a.eventFor(item.AppKey, platform, arch)
			event.ReleaseID = app.Latest.ID
			event.FromVersionCode = item.InstalledVersionCode
			event.ToVersionCode = app.Latest.VersionCode
			a.onceEvent(
				fmt.Sprintf("check:%s:%d", item.AppKey, app.Latest.VersionCode),
				withType(event, eventCheck),
			)
		}

		view.Apps = append(view.Apps, item)
	}
	return view, nil
}

// Install 下载、校验并安装指定产物。
func (a *App) Install(appKey string, artifactID int) (store.Installed, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	platform, arch := platformInfo()
	client := api.New(a.baseURL())
	response, err := client.Catalog(a.httpContext(), platform, arch)
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

	a.emit(appKey, "preparing", -1, "准备下载")

	// 一次安装对应一次下载动作：以下载/安装事件共用同一份上下文，
	// 去重键统一用产物 ID，重试或连点不会重复计数。
	installedCode := 0
	if record, ok := a.store.Get(appKey); ok {
		installedCode = record.VersionCode
	}
	event := a.eventFor(appKey, platform, arch)
	event.ReleaseID = app.Latest.ID
	event.ArtifactID = artifact.ID
	event.FromVersionCode = installedCode
	event.ToVersionCode = app.Latest.VersionCode
	eventKey := fmt.Sprintf("artifact:%d", artifact.ID)
	a.onceEvent(eventKey+":download_start", withType(event, eventDownloadStart))

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

	sha, err := a.download(appKey, artifact.DownloadURL, artifact.ID, filePath, artifact.Size)
	if err != nil {
		a.onceEvent(eventKey+":download_fail", withType(event, eventDownloadFail))
		a.emit(appKey, "error", -1, err.Error())
		return store.Installed{}, err
	}
	a.onceEvent(eventKey+":download_success", withType(event, eventDownloadSuccess))

	a.emit(appKey, "verifying", 100, "校验 SHA-256")
	if !strings.EqualFold(sha, artifact.SHA256) {
		a.onceEvent(eventKey+":verify_fail", withType(event, eventVerifyFail))
		message := fmt.Sprintf("校验失败：期望 %s，实际 %s", artifact.SHA256, sha)
		a.emit(appKey, "error", -1, message)
		return store.Installed{}, fmt.Errorf("%s", message)
	}

	// 启动器更新自身：运行中的 bundle 不能就地覆盖，交给后台脚本在本进程退出后替换。
	if appKey == "vakaohub" {
		a.emit(appKey, "installing", 100, "正在准备替换启动器")
		if err := install.SelfUpdate(artifact.Format, filePath); err != nil {
			a.onceEvent(eventKey+":install_fail", withType(event, eventInstallFail))
			a.emit(appKey, "error", -1, err.Error())
			return store.Installed{}, err
		}
		a.onceEvent(eventKey+":install_success", withType(event, eventInstallSuccess))
		current, _, _ := install.CurrentBundle()
		a.emit(appKey, "done", 100, "更新已就绪，重启启动器后完成替换")
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
	if record, ok := a.store.Get(appKey); ok {
		if _, statErr := os.Stat(record.InstallPath); statErr == nil {
			existingPath = record.InstallPath
		}
	}
	if existingPath == "" {
		if found := install.FindApps(app.Name); len(found) > 0 {
			existingPath = found[0].Path
		}
	}

	a.emit(appKey, "installing", 100, "正在安装")
	installPath, err := install.Install(
		artifact.Format, filePath, app.AppKey, app.Name, existingPath, a.installDir(),
	)
	if err != nil {
		a.onceEvent(eventKey+":install_fail", withType(event, eventInstallFail))
		a.emit(appKey, "error", -1, err.Error())
		return store.Installed{}, err
	}
	// msi / exe 拿不到安装结果，Install 返回 nil 只代表安装程序已正常启动，事件按成功计。
	a.onceEvent(eventKey+":install_success", withType(event, eventInstallSuccess))
	// 安装后让已安装索引重新扫描，下一次刷新能看到新条目。
	install.InvalidateIndex()

	if installPath == "" {
		// msi / exe 交给系统安装程序，拿不到可启动路径；不写空路径记录，
		// 否则下次刷新时 os.Stat("") 必然失败并删掉记录，表现为“装完仍显示未安装”。
		if artifact.Format == "exe" {
			a.emit(appKey, "done", 100, "已启动安装程序，请按向导完成安装")
		} else {
			a.emit(appKey, "done", 100, "安装完成；未记录启动路径，请从系统应用列表启动")
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
	if err := a.store.Put(item); err != nil {
		return store.Installed{}, err
	}

	a.emit(appKey, "done", 100, "安装完成")
	return item, nil
}

func (a *App) Uninstall(appKey string, deleteData bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if appKey == "vakaohub" {
		return fmt.Errorf("不能卸载正在运行的启动器")
	}

	item, ok := a.store.Get(appKey)
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
	_ = a.store.Remove(appKey)
	// 卸载后让已安装索引重新扫描，避免残留条目继续显示为已安装。
	install.InvalidateIndex()

	if deleteData {
		return install.RemoveData(bundleID)
	}
	return nil
}

func (a *App) Launch(appKey string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 启动器自身就是当前进程，再执行 open 只会打开安装记录里的旧路径（用户可能已挪动 bundle），
	// 这里直接视为已启动，避免误报“文件不存在”。
	if appKey == "vakaohub" {
		return nil
	}

	item, ok := a.store.Get(appKey)
	if !ok {
		return fmt.Errorf("尚未安装该应用")
	}
	if _, err := os.Stat(item.InstallPath); err != nil {
		// 记录路径失效时先重新扫描并修正记录，避免用户挪动应用后永远打不开。
		if found := install.FindApps(item.Name); len(found) > 0 {
			item.InstallPath = found[0].Path
			item.VersionName = found[0].Version
			item.VersionCode = 0
			_ = a.store.Put(item)
			install.InvalidateIndex()
		} else {
			_ = a.store.Remove(appKey)
			return fmt.Errorf("%s 已不在原安装位置，可能已被移动或删除；请在软件中心重新安装", item.Name)
		}
	}
	if err := install.Launch(item.InstallPath); err != nil {
		return err
	}

	// 每次成功启动都是一次真实使用行为，不做去重；版本未知时留空由前端显示为「-」。
	platform, arch := platformInfo()
	a.reportEvent(withType(a.eventFor(appKey, platform, arch), eventLaunch))
	return nil
}

// Reveal 在系统文件管理器中选中应用。
func (a *App) Reveal(appKey string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	path := ""
	if appKey == "vakaohub" {
		if current, _, ok := install.CurrentBundle(); ok {
			path = current
		}
	}
	if path == "" {
		item, ok := a.store.Get(appKey)
		if !ok {
			return fmt.Errorf("尚未安装该应用")
		}
		path = item.InstallPath
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("安装目录不存在：%s", path)
	}
	return install.Reveal(path)
}

func (a *App) OpenURL(url string) {
	if a.ctx != nil {
		wruntime.BrowserOpenURL(a.ctx, url)
	}
}

// Quit 由前端在自更新后调用：退出进程让后台脚本完成 bundle 替换并重新打开新版本。
func (a *App) Quit() {
	if a.ctx != nil {
		wruntime.Quit(a.ctx)
	}
}

func (a *App) emit(appKey, phase string, percent int, message string) {
	if a.ctx == nil {
		return
	}
	wruntime.EventsEmit(a.ctx, "install:progress", Progress{
		AppKey:  appKey,
		Phase:   phase,
		Percent: percent,
		Message: message,
	})
}

func (a *App) download(appKey, url string, artifactID int, dest string, expectedSize int64) (string, error) {
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

	request, err := http.NewRequestWithContext(a.httpContext(), http.MethodGet, url, nil)
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
					a.emit(appKey, "downloading", percent, "下载中")
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

	a.emit(appKey, "downloading", 100, "下载完成")
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

func platformInfo() (string, string) {
	platform := contract.PlatformLinux
	switch runtime.GOOS {
	case "darwin":
		platform = contract.PlatformDarwin
	case "windows":
		platform = contract.PlatformWindows
	}

	arch := contract.ArchUniversal
	switch runtime.GOARCH {
	case "arm64":
		arch = contract.ArchARM64
	case "amd64":
		arch = contract.ArchX64
	}
	return platform, arch
}

func pickArtifact(artifacts []api.Artifact, platform, arch string) *api.Artifact {
	preference := contract.PreferredFormats[platform]

	var best *api.Artifact
	bestScore := 1 << 30
	for i := range artifacts {
		artifact := &artifacts[i]
		if artifact.Platform != platform {
			continue
		}
		if artifact.Arch != arch && artifact.Arch != contract.ArchUniversal {
			continue
		}

		archScore := 1
		if artifact.Arch == arch {
			archScore = 0
		}
		formatScore := len(preference)
		for index, format := range preference {
			if artifact.Format == format {
				formatScore = index
				break
			}
		}
		score := archScore*100 + formatScore
		if score < bestScore {
			bestScore = score
			best = artifact
		}
	}
	return best
}
