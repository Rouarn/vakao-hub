package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"vakaohub/launcher/internal/apperr"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (c *Client) Catalog(ctx context.Context, platform, arch string) (CatalogResponse, error) {
	endpoint := fmt.Sprintf(
		"%s/catalog?platform=%s&arch=%s",
		c.BaseURL,
		url.QueryEscape(platform),
		url.QueryEscape(arch),
	)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return CatalogResponse{}, apperr.New(
			apperr.KindInternal,
			"catalog.request",
			"服务地址无效，请在设置中检查服务地址",
			err,
		)
	}

	response, err := c.HTTP.Do(request)
	if err != nil {
		return CatalogResponse{}, apperr.FromContext(err, "catalog")
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return CatalogResponse{}, apperr.FromContext(err, "catalog.read")
	}

	var payload envelope
	if err := json.Unmarshal(body, &payload); err != nil {
		return CatalogResponse{}, apperr.FromHTTPStatus(response.StatusCode, "catalog.decode", "")
	}
	if response.StatusCode != http.StatusOK || payload.Code != http.StatusOK {
		status := response.StatusCode
		// 2xx 但业务码失败：按不可重试的请求错误处理，不把 200 当成 5xx。
		if status < http.StatusBadRequest {
			status = http.StatusBadRequest
		}
		return CatalogResponse{}, apperr.FromHTTPStatus(status, "catalog", payload.Message)
	}

	var catalog CatalogResponse
	if err := json.Unmarshal(payload.Data, &catalog); err != nil {
		return CatalogResponse{}, apperr.FromHTTPStatus(response.StatusCode, "catalog.decode", "")
	}
	return catalog, nil
}
