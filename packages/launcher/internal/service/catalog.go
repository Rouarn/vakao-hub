package service

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"vakaohub/launcher/internal/api"
	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/contract"
	"vakaohub/launcher/internal/install"
	"vakaohub/launcher/internal/store"
)

type CatalogService struct {
	host *Host
}

func NewCatalog(host *Host) *CatalogService {
	return &CatalogService{host: host}
}

func (s *CatalogService) GetCatalog(ctx context.Context) (CatalogView, error) {
	_, release, err := s.host.beginOp()
	if err != nil {
		return CatalogView{}, err
	}
	defer release()

	ctx, cancel := context.WithTimeout(ctx, s.host.timeouts.Catalog)
	defer cancel()
	return s.catalogLocked(ctx)
}

func (s *CatalogService) catalogLocked(ctx context.Context) (CatalogView, error) {
	platform, arch := platformInfo()
	client := s.host.newClient()
	response, err := client.Catalog(ctx, platform, arch)
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
				if record, exists := s.host.store.Get(app.AppKey); !exists ||
					record.InstallPath != path || record.VersionName != version {
					_ = s.host.store.Put(store.Installed{
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
			if record, ok := s.host.store.Get(app.AppKey); ok {
				if _, statErr := os.Stat(record.InstallPath); statErr == nil {
					item.Installed = true
					item.InstallPath = record.InstallPath
					installedPath = record.InstallPath
					installedVersion = record.VersionName
					installedCode = record.VersionCode
				} else {
					_ = s.host.store.Remove(app.AppKey)
				}
			}
		}

		if !item.Installed && len(detected) > 0 {
			item.Installed = true
			item.InstallPath = detected[0].Path
			installedPath = detected[0].Path
			installedVersion = detected[0].Version
			_ = s.host.store.Put(store.Installed{
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
			event := s.host.eventFor(item.AppKey, platform, arch)
			event.ReleaseID = app.Latest.ID
			event.FromVersionCode = item.InstalledVersionCode
			event.ToVersionCode = app.Latest.VersionCode
			s.host.onceEvent(
				fmt.Sprintf("check:%s:%d", item.AppKey, app.Latest.VersionCode),
				withType(event, eventCheck),
			)
		}

		view.Apps = append(view.Apps, item)
	}
	return view, nil
}

func (s *CatalogService) Launch(appKey string) error {
	_, release, err := s.host.beginOp()
	if err != nil {
		return err
	}
	defer release()

	// 启动器自身就是当前进程，再执行 open 只会打开安装记录里的旧路径（用户可能已挪动 bundle），
	// 这里直接视为已启动，避免误报“文件不存在”。
	if appKey == "vakaohub" {
		return nil
	}

	item, ok := s.host.store.Get(appKey)
	if !ok {
		return apperr.New(apperr.KindInternal, "launch", "尚未安装该应用", nil)
	}
	if _, err := os.Stat(item.InstallPath); err != nil {
		// 记录路径失效时先重新扫描并修正记录，避免用户挪动应用后永远打不开。
		if found := install.FindApps(item.Name); len(found) > 0 {
			item.InstallPath = found[0].Path
			item.VersionName = found[0].Version
			item.VersionCode = 0
			_ = s.host.store.Put(item)
			install.InvalidateIndex()
		} else {
			_ = s.host.store.Remove(appKey)
			return apperr.New(
				apperr.KindInternal,
				"launch",
				fmt.Sprintf("%s 已不在原安装位置，可能已被移动或删除；请在软件中心重新安装", item.Name),
				err,
			)
		}
	}
	if err := install.Launch(item.InstallPath); err != nil {
		return err
	}

	// 每次成功启动都是一次真实使用行为，不做去重；版本未知时留空由前端显示为「-」。
	platform, arch := platformInfo()
	s.host.reportEvent(withType(s.host.eventFor(appKey, platform, arch), eventLaunch))
	return nil
}

// Reveal 在系统文件管理器中选中应用。
func (s *CatalogService) Reveal(appKey string) error {
	_, release, err := s.host.beginOp()
	if err != nil {
		return err
	}
	defer release()

	path := ""
	if appKey == "vakaohub" {
		if current, _, ok := install.CurrentBundle(); ok {
			path = current
		}
	}
	if path == "" {
		item, ok := s.host.store.Get(appKey)
		if !ok {
			return apperr.New(apperr.KindInternal, "reveal", "尚未安装该应用", nil)
		}
		path = item.InstallPath
	}
	if _, err := os.Stat(path); err != nil {
		return apperr.New(apperr.KindInternal, "reveal", fmt.Sprintf("安装目录不存在：%s", path), err)
	}
	if err := install.Reveal(path); err != nil {
		return apperr.New(apperr.KindInternal, "reveal", "无法打开安装位置，请稍后重试", err)
	}
	return nil
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
