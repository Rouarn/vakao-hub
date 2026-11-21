package service

import "vakaohub/launcher/internal/api"

// Progress 通过 Wails 事件推送给前端。
type Progress struct {
	AppKey      string `json:"appKey"`
	Phase       string `json:"phase"`
	Percent     int    `json:"percent"`
	Message     string `json:"message"`
	OperationID string `json:"operationID"`
	Retryable   bool   `json:"retryable"`
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

// UpdateNotice 是上一次自更新的结果，启动时提示一次后删除。
type UpdateNotice struct {
	Status    string `json:"status"`
	Message   string `json:"message"`
	UpdatedAt int64  `json:"updatedAt"`
}
