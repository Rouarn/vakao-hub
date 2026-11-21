package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"vakaohub/launcher/internal/apperr"
)

// EventReport 对应服务端 ReportEventDto。零值字段用 omitempty 省略：
// releaseId/artifactId 为 0 时不上报，服务端按可选字段处理。
type EventReport struct {
	AppKey          string `json:"appKey"`
	Platform        string `json:"platform"`
	Arch            string `json:"arch,omitempty"`
	Type            string `json:"type"`
	DeviceID        string `json:"deviceId,omitempty"`
	ReleaseID       int    `json:"releaseId,omitempty"`
	ArtifactID      int    `json:"artifactId,omitempty"`
	FromVersionCode int    `json:"fromVersionCode,omitempty"`
	ToVersionCode   int    `json:"toVersionCode,omitempty"`
}

// ReportEvent 上报一条客户端事件。服务端校验不过会静默返回成功，
// 因此这里只把网络/HTTP 层错误交回调用方，由调用方决定是否忽略。
func (c *Client) ReportEvent(ctx context.Context, event EventReport) error {
	body, err := json.Marshal(event)
	if err != nil {
		return apperr.New(apperr.KindInternal, "report.encode", "", err)
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.BaseURL+"/updates/report",
		bytes.NewReader(body),
	)
	if err != nil {
		return apperr.New(apperr.KindInternal, "report.request", "", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := c.HTTP.Do(request)
	if err != nil {
		return apperr.FromContext(err, "report")
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		return apperr.FromHTTPStatus(response.StatusCode, "report", "")
	}
	return nil
}
