package aibot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// DefaultWSURL 是智能机器人长连接的服务端地址。
const DefaultWSURL = "wss://openws.work.weixin.qq.com"

const (
	pingInterval   = 30 * time.Second
	writeWait      = 10 * time.Second
	pongWait       = 75 * time.Second // 大于两个心跳周期，避免正常抖动被误判
	readLimitBytes = 1 << 20
	// 重连退避。同一个机器人只能有一条长连接，新订阅会踢掉旧的，
	// 因此退避不仅要防打爆服务端，也要避免两个实例互踢。
	backoffMin = 3 * time.Second
	backoffMax = 60 * time.Second
)

// Callback 接收解出来的回调。
//
// reqID 必须原样传回来才能回复：长连接的回复是「回给某次回调」，
// 不是「回给某个人」，丢了它就发不出去。
type Callback interface {
	OnMessage(ctx context.Context, reqID string, body *InboundBody)
	OnEvent(ctx context.Context, reqID string, body *EventBody)
}

// Client 维护一条到企业微信的长连接：订阅、心跳、读帧、断线重连。
//
// 它同时充当 bot.Sender：回复走 Reply(reqID, content)，与回调的 req_id 绑定。
type Client struct {
	botID  string
	secret string
	wsURL  string
	cb     Callback

	mu   sync.Mutex
	conn *websocket.Conn // 当前连接；重连期间为 nil
}

func NewClient(botID, secret, wsURL string, cb Callback) *Client {
	if wsURL == "" {
		wsURL = DefaultWSURL
	}
	return &Client{botID: botID, secret: secret, wsURL: wsURL, cb: cb}
}

// SetCallback 在构造之后注入回调。
//
// 构造时通常只能传 nil：回调实现需要持有客户端本身（回复要用它），
// 先建客户端再挂回调可以避免绕成一个环。
func (c *Client) SetCallback(cb Callback) {
	c.mu.Lock()
	c.cb = cb
	c.mu.Unlock()
}

// callback 取出当前回调。
//
// 必须取出后立刻释放锁再调用：write 全程持锁，而回调里会调用 Reply →
// write，若持锁调用回调就会自锁死。
func (c *Client) callback() Callback {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cb
}

// Run 阻塞运行直到 ctx 结束，内部自动重连。返回 ctx 的错误。
func (c *Client) Run(ctx context.Context) error {
	backoff := backoffMin
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := c.session(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Printf("aibot: 连接结束（%v），%s 后重连", err, backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		// 连上并稳定运行过就重置退避；这里用简单的翻倍策略。
		backoff *= 2
		if backoff > backoffMax {
			backoff = backoffMax
		}
	}
}

// session 建立一条连接并跑到它断开为止。
func (c *Client) session(ctx context.Context) error {
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	conn, resp, err := dialer.DialContext(ctx, c.wsURL, nil)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("握手失败（HTTP %d）：%w", resp.StatusCode, err)
		}
		return fmt.Errorf("握手失败：%w", err)
	}
	defer conn.Close()

	conn.SetReadLimit(readLimitBytes)
	c.setConn(conn)
	defer c.setConn(nil)

	// 订阅。失败说明 BotID/Secret 不对，重连也救不回来，但要如实报出来。
	sub := NewSubscribe(newReqID("sub"), c.botID, c.secret)
	if err := c.write(ctx, conn, sub); err != nil {
		return fmt.Errorf("发送订阅失败：%w", err)
	}

	// 心跳独立协程：久不发 ping 服务端会主动断开。
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go c.heartbeat(heartbeatCtx, conn)

	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		c.dispatch(ctx, raw)
	}
}

func (c *Client) heartbeat(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// 用协议层 ping 帧保活（WriteControl 不是数据帧）。
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait)); err != nil {
				return
			}
			// 官方心跳是应用层命令 ping，与协议层 ping 并存。
			if err := c.write(ctx, conn, NewPing(newReqID("ping"))); err != nil {
				return
			}
		}
	}
}

// dispatch 解析一帧并分派。控制帧与响应帧（订阅/ping 的回执）只记日志。
func (c *Client) dispatch(ctx context.Context, raw []byte) {
	var f Frame
	if err := json.Unmarshal(raw, &f); err != nil {
		log.Printf("aibot: 收到无法解析的帧：%v", err)
		return
	}
	cb := c.callback()

	switch f.Cmd {
	case CmdMsgCallback:
		body, err := f.DecodeBody()
		if err != nil {
			log.Printf("aibot: 消息帧解析失败：%v", err)
			return
		}
		if f.Headers.ReqID == "" {
			// 没有 req_id 就没法回复，只能丢弃并记下来。
			log.Printf("aibot: 消息缺少 req_id，无法回复，已丢弃 msgid=%s", body.MsgID)
			return
		}
		if cb == nil {
			log.Printf("aibot: 尚未挂载回调，丢弃消息 msgid=%s", body.MsgID)
			return
		}
		cb.OnMessage(ctx, f.Headers.ReqID, body)

	case CmdEventCallback:
		var body EventBody
		if err := json.Unmarshal(f.Body, &body); err != nil {
			log.Printf("aibot: 事件帧解析失败：%v", err)
			return
		}
		if cb != nil {
			cb.OnEvent(ctx, f.Headers.ReqID, &body)
		}

	default:
		// 订阅与心跳的回执走这里。errcode 非 0 要显式报出来。
		if f.ErrCode != 0 {
			log.Printf("aibot: %s 返回错误 errcode=%d errmsg=%s", f.Cmd, f.ErrCode, f.ErrMsg)
		}
	}
}

// Reply 回复一次回调。实现 bot.Sender。
func (c *Client) Reply(ctx context.Context, reqID, content string) error {
	if reqID == "" {
		return errors.New("缺少 req_id，无法回复")
	}
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return errors.New("长连接当前不可用")
	}
	return c.write(ctx, conn, NewTextReply(reqID, content))
}

// write 串行化写操作：gorilla 不允许并发写同一个连接。
func (c *Client) write(ctx context.Context, conn *websocket.Conn, f *Frame) error {
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, raw)
}

func (c *Client) setConn(conn *websocket.Conn) {
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
}

// newReqID 生成本次请求的 req_id。官方 SDK 用的形如 ping_<毫秒>_<hex>，
// 这里保持同样的可读形状，便于抓包时对上号。
func newReqID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s_%d_%s", prefix, time.Now().UnixMilli(), hex.EncodeToString(b[:]))
}
