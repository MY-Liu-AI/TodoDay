package aibot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Verify 依赖的真实协议行为（实测）：企业微信对订阅/心跳的回执**没有 cmd 字段**，
// 只有 headers.req_id + errcode + errmsg，按 req_id 和请求对上号。
// 这一组测试用本地假服务端锁定「按 req_id 找回执」这件事，防止有人改回按 cmd 匹配。

// verifyServer 起一个回执服务端：读到订阅后，回一条无 cmd 的回执。
func verifyServer(t *testing.T, errcode int, errmsg string, preReply func(conn *websocket.Conn, subReqID string)) *httptest.Server {
	t.Helper()
	up := newUpgrader()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var f Frame
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Errorf("订阅帧不是合法 JSON：%v", err)
			return
		}
		if f.Headers.ReqID == "" {
			t.Error("订阅帧缺少 req_id")
		}

		if preReply != nil {
			preReply(conn, f.Headers.ReqID)
		}

		ack, _ := json.Marshal(map[string]any{
			"headers": map[string]string{"req_id": f.Headers.ReqID},
			"errcode": errcode,
			"errmsg":  errmsg,
		})
		_ = conn.WriteMessage(websocket.TextMessage, ack)
		// 给客户端一点读完再关闭的时间。
		time.Sleep(100 * time.Millisecond)
	}))
}

func TestVerifySuccess(t *testing.T) {
	srv := verifyServer(t, 0, "ok", nil)
	defer srv.Close()

	ok, errcode, _, err := Verify(context.Background(), "BOT", "SECRET", wsURLOf(srv))
	if err != nil {
		t.Fatalf("Verify 不应报错：%v", err)
	}
	if !ok || errcode != 0 {
		t.Fatalf("应订阅成功，得到 ok=%v errcode=%d", ok, errcode)
	}
}

func TestVerifyRejected(t *testing.T) {
	srv := verifyServer(t, 853000, "invalid bot_id or secret", nil)
	defer srv.Close()

	ok, errcode, errmsg, err := Verify(context.Background(), "BOT", "SECRET", wsURLOf(srv))
	if err != nil {
		t.Fatalf("被拒不应走 err（是正常失败结果）：%v", err)
	}
	if ok || errcode != 853000 {
		t.Fatalf("应订阅被拒，得到 ok=%v errcode=%d", ok, errcode)
	}
	if !strings.Contains(errmsg, "invalid") {
		t.Fatalf("errmsg 应带拒绝原因，得到 %q", errmsg)
	}
}

// 回执里若先来一条别的 req_id 的帧（比如订阅前的心跳），Verify 必须等
// 自己那条订阅的 req_id 出现，不能拿第一帧当结果。
func TestVerifyMatchesItsOwnReqID(t *testing.T) {
	srv := verifyServer(t, 0, "ok", func(conn *websocket.Conn, subReqID string) {
		// 先推一条与订阅无关的帧（req_id 不同）。
		other, _ := json.Marshal(map[string]any{
			"headers": map[string]string{"req_id": "someone-else"},
			"errcode": 0,
			"errmsg":  "ok",
		})
		_ = conn.WriteMessage(websocket.TextMessage, other)
	})
	defer srv.Close()

	ok, _, _, err := Verify(context.Background(), "BOT", "SECRET", wsURLOf(srv))
	if err != nil {
		t.Fatalf("Verify 不应报错：%v", err)
	}
	if !ok {
		t.Fatal("应订阅成功：Verify 必须按自己的 req_id 找回执")
	}
}
