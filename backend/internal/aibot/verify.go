package aibot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

// Verify 做一次真实的订阅握手，供设置页的「测试连接」按钮调用。
//
// 它拨一条新连接、发订阅、读回执，然后立刻关闭 —— 只验证 BotID/Secret
// 能否被企业微信接受，不常驻、不接收消息。ok 为 true 表示订阅回执
// errcode == 0（凭据有效）。失败时若服务端明确拒绝，errcode/errmsg 带上
// 拒绝细节；网络或协议层面的失败走 err。
func Verify(ctx context.Context, botID, secret, wsURL string) (ok bool, errcode int, errmsg string, err error) {
	if botID == "" || secret == "" {
		return false, 0, "", errors.New("缺少 BotID 或 Secret")
	}
	if wsURL == "" {
		wsURL = DefaultWSURL
	}

	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	conn, resp, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		if resp != nil {
			return false, 0, "", fmt.Errorf("握手失败（HTTP %d）：%w", resp.StatusCode, err)
		}
		return false, 0, "", fmt.Errorf("握手失败：%w", err)
	}
	defer conn.Close()

	if err := conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
		return false, 0, "", err
	}
	reqID := newReqID("verify")
	if err := conn.WriteJSON(NewSubscribe(reqID, botID, secret)); err != nil {
		return false, 0, "", fmt.Errorf("发送订阅失败：%w", err)
	}

	// 订阅回执**没有 cmd 字段**：企业微信对 aibot_subscribe / ping 的响应只有
	// headers.req_id + errcode + errmsg（实测：{"headers":{"req_id":...},"errcode":0,"errmsg":"ok"}）。
	// 所以不能按 cmd 找回执，要按 req_id 对上号。
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return false, 0, "", fmt.Errorf("等待订阅回执失败：%w", err)
		}
		var f Frame
		if err := json.Unmarshal(raw, &f); err != nil {
			continue
		}
		if f.Headers.ReqID != reqID {
			// 不是本次订阅的回执（理论上新鲜连接只会收到这一条），继续等。
			continue
		}
		if f.ErrCode != 0 {
			return false, f.ErrCode, f.ErrMsg, nil
		}
		return true, 0, f.ErrMsg, nil
	}
}
