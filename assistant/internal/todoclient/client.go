// Package todoclient 是 TodoDay 后端的 HTTP 客户端。
//
// 助手不直连数据库：Go 的 internal 导入规则是目录级的，同级目录无法
// import backend/internal/store。走 HTTP 反而是好事——字段校验、工作区
// 归属校验、幂等语义全部复用服务端实现，且两个进程不会争抢 SQLite 写锁。
package todoclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"time"

	"assistant/internal/parse"
)

// 错误分类。回执文案要据此区分「配置问题」与「内容问题」——
// 工作区 ID 配错时如果说成「任务内容不合法」，排查方向就完全错了。
var (
	ErrAuth       = errors.New("登录被拒绝")
	ErrForbidden  = errors.New("无权访问该工作区")
	ErrValidation = errors.New("内容不符合要求")
)

type Client struct {
	base        string
	username    string
	password    string
	workspaceID int64
	http        *http.Client
}

// New 构造客户端。使用 cookie jar 保存会话，与浏览器的行为一致。
func New(base, username, password string, workspaceID int64) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &Client{
		base:        base,
		username:    username,
		password:    password,
		workspaceID: workspaceID,
		http:        &http.Client{Jar: jar, Timeout: 15 * time.Second},
	}, nil
}

// WorkspaceID 返回配置的目标工作区。
func (c *Client) WorkspaceID() int64 { return c.workspaceID }

// Login 建立会话。启动时调用一次，失败即说明配置有误，应当直接退出。
func (c *Client) Login(ctx context.Context) error {
	body := map[string]string{"username": c.username, "password": c.password}
	resp, err := c.do(ctx, http.MethodPost, "/api/auth/login", body)
	if err != nil {
		return fmt.Errorf("连接后端失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w：状态码 %d", ErrAuth, resp.StatusCode)
	}
	return nil
}

// apiError 是后端返回的错误体。
type apiError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// BotCredentials 是后端存的助手凭据（明文，仅 owner 能取）。
type BotCredentials struct {
	Enabled    bool   `json:"enabled"`
	BotID      string `json:"botId"`
	BotSecret  string `json:"botSecret"`
	KimiAPIKey string `json:"kimiApiKey"`
	KimiBase   string `json:"kimiBase"`
	KimiModel  string `json:"kimiModel"`
}

// ErrNotConfigured 表示后端尚未配置助手。调用方据此回落到环境变量。
var ErrNotConfigured = errors.New("后端尚未配置助手")

// FetchCredentials 从后端取助手凭据。
//
// 这是用户在网页上填的配置能生效的通道：助手每次（重）连前都来取一次，
// 改了配置不用重启进程。404 单独映射成 ErrNotConfigured，让调用方能
// 区分「没配」和「出错」。
func (c *Client) FetchCredentials(ctx context.Context) (*BotCredentials, error) {
	path := fmt.Sprintf("/api/workspaces/%d/assistant/credentials", c.workspaceID)
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("连接后端失败：%w", err)
	}
	// 会话过期：重登一次再试。
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		if err := c.Login(ctx); err != nil {
			return nil, fmt.Errorf("%w（自动重登失败）", ErrAuth)
		}
		resp, err = c.do(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, fmt.Errorf("连接后端失败：%w", err)
		}
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var out BotCredentials
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return nil, fmt.Errorf("凭据响应不是合法 JSON：%w", err)
		}
		return &out, nil
	case http.StatusNotFound:
		return nil, ErrNotConfigured
	case http.StatusForbidden:
		var body apiError
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return nil, fmt.Errorf("%w：%s", ErrForbidden, body.Message)
	default:
		var body apiError
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return nil, fmt.Errorf("读取凭据失败（%d）：%s", resp.StatusCode, body.Message)
	}
}

// WorkspaceTimezone 读取目标工作区的时区。
//
// 相对日期必须按工作区所在时区解析：服务器跑在 UTC 而工作区是
// Asia/Shanghai 时，「明天」会差一天——这种错误很难从现象上看出来。
func (c *Client) WorkspaceTimezone(ctx context.Context) (string, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/me", nil)
	if err != nil {
		return "", fmt.Errorf("连接后端失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("读取工作区信息失败：状态码 %d", resp.StatusCode)
	}
	var out struct {
		Workspaces []struct {
			ID       int64  `json:"id"`
			Timezone string `json:"timezone"`
		} `json:"workspaces"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("工作区信息不是合法 JSON：%w", err)
	}
	for _, ws := range out.Workspaces {
		if ws.ID == c.workspaceID {
			if ws.Timezone == "" {
				return "Asia/Shanghai", nil
			}
			return ws.Timezone, nil
		}
	}
	return "", fmt.Errorf("%w：账号不属于工作区 %d", ErrForbidden, c.workspaceID)
}

// CreatePlan 建一条任务。clientID 用于幂等：企业微信重投同一条消息时，
// 服务端会返回既有任务而不是新建一条。
func (c *Client) CreatePlan(ctx context.Context, plan parse.Plan, clientID string) error {
	payload := map[string]any{
		"t":        plan.Title,
		"p":        plan.Priority,
		"due":      plan.Due,
		"g":        plan.Group,
		"clientId": clientID,
	}
	path := fmt.Sprintf("/api/workspaces/%d/tasks", c.workspaceID)

	resp, err := c.do(ctx, http.MethodPost, path, payload)
	if err != nil {
		return fmt.Errorf("连接后端失败：%w", err)
	}
	// 401：会话过期，重登一次再试。用同一个 clientID，重试也不会产生重复任务。
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		if err := c.Login(ctx); err != nil {
			return fmt.Errorf("%w（自动重登失败）", ErrAuth)
		}
		resp, err = c.do(ctx, http.MethodPost, path, payload)
		if err != nil {
			return fmt.Errorf("连接后端失败：%w", err)
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
		return nil
	}

	var body apiError
	_ = json.NewDecoder(resp.Body).Decode(&body)
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return ErrAuth
	case http.StatusForbidden:
		return fmt.Errorf("%w：%s", ErrForbidden, body.Message)
	case http.StatusBadRequest:
		return fmt.Errorf("%w：%s", ErrValidation, body.Message)
	default:
		return fmt.Errorf("后端返回 %d：%s", resp.StatusCode, body.Message)
	}
}

// do 发一个 JSON 请求，自动带上 CSRF 头 —— 缺少它后端会一律 403，
// 而且错误码看起来像权限问题，很容易误判。
func (c *Client) do(ctx context.Context, method, path string, payload any) (*http.Response, error) {
	var reader *bytes.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	return c.http.Do(req)
}
