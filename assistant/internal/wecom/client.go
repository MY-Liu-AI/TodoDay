package wecom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// apiBase 是企业微信 API 的根地址。测试时替换为 httptest 的地址。
var apiBase = "https://qyapi.weixin.qq.com"

const tokenSkew = 5 * time.Minute

type tokenResponse struct {
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

// Client 负责换取 access_token 并主动发消息。
//
// 用主动推送而不是「被动加密回复」的原因：回调必须在 5 秒内响应，
// 超时企业微信会重试 3 次；而解析一句自然语言要调 Claude，耗时不可控。
// 所以回调里只做解密（微秒级）并立刻回 success，回执另发。
type Client struct {
	corpID  string
	secret  string
	agentID string
	http    *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
}

func NewClient(corpID, secret, agentID string) *Client {
	return &Client{
		corpID:  corpID,
		secret:  secret,
		agentID: agentID,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// AccessToken 返回可用的 access_token，带内存缓存。
// 官方有效期为 7200 秒，这里提前 5 分钟失效，避免边界上用到过期令牌。
func (c *Client) AccessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.expires) {
		return c.token, nil
	}

	url := fmt.Sprintf("%s/cgi-bin/gettoken?corpid=%s&corpsecret=%s", apiBase, c.corpID, c.secret)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("获取 access_token 失败：%w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var out tokenResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("access_token 响应不是合法 JSON：%w", err)
	}
	if out.ErrCode != 0 {
		return "", fmt.Errorf("获取 access_token 被拒绝：errcode=%d errmsg=%s", out.ErrCode, out.ErrMsg)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("access_token 为空")
	}

	ttl := time.Duration(out.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}
	c.token = out.AccessToken
	c.expires = time.Now().Add(ttl - tokenSkew)
	return c.token, nil
}

// invalidate 在服务端告知令牌失效时清掉缓存，下次调用会重新换取。
func (c *Client) invalidate() {
	c.mu.Lock()
	c.token = ""
	c.expires = time.Time{}
	c.mu.Unlock()
}

type sendRequest struct {
	ToUser  string `json:"touser"`
	MsgType string `json:"msgtype"`
	AgentID string `json:"agentid"`
	Text    struct {
		Content string `json:"content"`
	} `json:"text"`
}

type sendResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

// SendText 给指定成员发一条文本消息。
// 42001 表示 access_token 过期，此时刷新缓存并重试一次。
func (c *Client) SendText(ctx context.Context, toUser, content string) error {
	for attempt := 0; attempt < 2; attempt++ {
		token, err := c.AccessToken(ctx)
		if err != nil {
			return err
		}
		code, msg, err := c.post(ctx, token, toUser, content)
		if err != nil {
			return err
		}
		if code == 0 {
			return nil
		}
		if code == 42001 || code == 40014 {
			c.invalidate()
			continue
		}
		return fmt.Errorf("发送消息被拒绝：errcode=%d errmsg=%s", code, msg)
	}
	return fmt.Errorf("发送消息失败：access_token 反复失效")
}

func (c *Client) post(ctx context.Context, token, toUser, content string) (int, string, error) {
	var payload sendRequest
	payload.ToUser = toUser
	payload.MsgType = "text"
	payload.AgentID = c.agentID
	payload.Text.Content = content

	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, "", err
	}
	url := fmt.Sprintf("%s/cgi-bin/message/send?access_token=%s", apiBase, token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("发送消息失败：%w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var out sendResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return 0, "", fmt.Errorf("发消息响应不是合法 JSON：%w", err)
	}
	return out.ErrCode, out.ErrMsg, nil
}
