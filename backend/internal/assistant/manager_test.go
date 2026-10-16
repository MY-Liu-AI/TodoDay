package assistant

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"tododay/internal/aibot"
	"tododay/internal/secretbox"
	"tododay/internal/store"
)

// 内嵌 Manager 的测试。重点不是连接细节（那在 aibot 的 client_test 里），
// 而是三条安全不变式的落点：凭据来源三态分离、撤销即断开、启动/停止状态。

const managerTestKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

func newManagerStore(t *testing.T) (*store.Store, *secretbox.Box, int64, int64) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser(t.Context(), "owner", "hash")
	if err != nil {
		t.Fatal(err)
	}
	ws, err := st.CreateWorkspaceWithOwner(t.Context(), u.ID, "测试工作区", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	box, err := secretbox.FromKey(managerTestKey)
	if err != nil {
		t.Fatal(err)
	}
	return st, box, ws.ID, u.ID
}

// saveConfig 写入一份配置。secret 留空表示不写机器人 Secret。
func saveConfig(t *testing.T, st *store.Store, box *secretbox.Box, wid, uid int64, enabled bool, botID, secret string) {
	t.Helper()
	in := store.AssistantConfigInput{Enabled: enabled, BotID: botID}
	if secret != "" && box != nil {
		enc, err := box.Seal(secret)
		if err != nil {
			t.Fatal(err)
		}
		in.BotSecretEnc = enc
	}
	if _, err := st.SaveAssistantConfig(t.Context(), wid, uid, in, 0); err != nil {
		t.Fatal(err)
	}
}

// credentialSource 是三态分离的落点。混为一谈就会退回「撤销了还在跑」的
// fail-open，所以这里逐态钉死。
func TestManagerCredentialSourceThreeState(t *testing.T) {
	st, box, wid, uid := newManagerStore(t)
	m := NewManager(st, box)
	src := m.credentialSource(wid)
	ctx := t.Context()

	// 未配置 → 没有凭据。
	if _, _, err := src(ctx); !errors.Is(err, aibot.ErrNoCredentials) {
		t.Fatalf("未配置应返回 ErrNoCredentials，得到 %v", err)
	}

	// 停用 → 明确区分于「没有凭据」。
	saveConfig(t, st, box, wid, uid, false, "B1", "s")
	if _, _, err := src(ctx); !errors.Is(err, aibot.ErrDisabled) {
		t.Fatalf("停用应返回 ErrDisabled，得到 %v", err)
	}

	// 已启用但没有凭据 → ErrNoCredentials。
	saveConfig(t, st, box, wid, uid, true, "", "")
	if _, _, err := src(ctx); !errors.Is(err, aibot.ErrNoCredentials) {
		t.Fatalf("无凭据应返回 ErrNoCredentials，得到 %v", err)
	}

	// 已启用且有凭据 → 返回明文。
	saveConfig(t, st, box, wid, uid, true, "BOT-1", "the-secret")
	botID, secret, err := src(ctx)
	if err != nil || botID != "BOT-1" || secret != "the-secret" {
		t.Fatalf("应返回明文凭据，得到 %q/%q/%v", botID, secret, err)
	}
}

// 主密钥缺失（比如换机器后没设 TODO_CONFIG_KEY）时，即使库里有密文，
// 也不能解密 —— 必须判为没有凭据，而不是崩掉或拿旧值。
func TestManagerCredentialSourceWithoutBox(t *testing.T) {
	st, box, wid, uid := newManagerStore(t)
	saveConfig(t, st, box, wid, uid, true, "BOT-1", "the-secret")

	m := NewManager(st, nil) // 刻意不装主密钥
	_, _, err := m.credentialSource(wid)(t.Context())
	if !errors.Is(err, aibot.ErrNoCredentials) {
		t.Fatalf("缺主密钥应返回 ErrNoCredentials，得到 %v", err)
	}
}

// Start 的前置校验必须逐个拒绝，且都不该真的去连接。
func TestManagerStartValidation(t *testing.T) {
	st, box, wid, uid := newManagerStore(t)
	ctx := t.Context()

	// 未配置。
	m := NewManager(st, box)
	if err := m.Start(ctx, wid, uid); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("未配置应返回 ErrNotConfigured，得到 %v", err)
	}

	// 停用。
	saveConfig(t, st, box, wid, uid, false, "B1", "s")
	if err := m.Start(ctx, wid, uid); !errors.Is(err, aibot.ErrDisabled) {
		t.Fatalf("停用应返回 ErrDisabled，得到 %v", err)
	}

	// 已启用但没有凭据。
	saveConfig(t, st, box, wid, uid, true, "", "")
	if err := m.Start(ctx, wid, uid); !errors.Is(err, aibot.ErrNoCredentials) {
		t.Fatalf("无凭据应返回 ErrNoCredentials，得到 %v", err)
	}

	// 有密文但没有主密钥。
	saveConfig(t, st, box, wid, uid, true, "B1", "s")
	mNoBox := NewManager(st, nil)
	if err := mNoBox.Start(ctx, wid, uid); !errors.Is(err, ErrEncryptionUnavailable) {
		t.Fatalf("缺主密钥应返回 ErrEncryptionUnavailable，得到 %v", err)
	}
}

// 一个只做「升级 + 收订阅 + 回 ok」的本地假服务，让启动/停止能被证明，
// 而不用真的去连企业微信。
func newWSServer(t *testing.T, gotSub chan<- string) *httptest.Server {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
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
		var f struct {
			Cmd     string `json:"cmd"`
			Headers struct {
				ReqID string `json:"req_id"`
			} `json:"headers"`
		}
		if json.Unmarshal(raw, &f) == nil && gotSub != nil {
			select {
			case gotSub <- string(raw):
			default:
			}
		}
		ack, _ := json.Marshal(map[string]any{
			"cmd":     "aibot_subscribe",
			"headers": map[string]string{"req_id": f.Headers.ReqID},
			"errcode": 0,
			"errmsg":  "ok",
		})
		_ = conn.WriteMessage(websocket.TextMessage, ack)

		// 保持连接直到客户端（Stop）断开。
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
}

func wsURL(srv *httptest.Server) string { return "ws" + strings.TrimPrefix(srv.URL, "http") }

func TestManagerStartStopRoundTrip(t *testing.T) {
	st, box, wid, uid := newManagerStore(t)
	gotSub := make(chan string, 1)
	srv := newWSServer(t, gotSub)
	defer srv.CloseClientConnections()
	defer srv.Close()

	saveConfig(t, st, box, wid, uid, true, "BOT-1", "the-secret")

	m := NewManager(st, box)
	m.wsURL = wsURL(srv)
	defer m.Close()

	if m.IsRunning(wid) {
		t.Fatal("启动前不应报告运行中")
	}
	if err := m.Start(t.Context(), wid, uid); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	if !m.IsRunning(wid) {
		t.Fatal("启动后应报告运行中")
	}
	// 幂等：再启动一次仍是运行中，不重复建连接。
	if err := m.Start(t.Context(), wid, uid); err != nil {
		t.Fatalf("重复启动应幂等成功：%v", err)
	}

	// 订阅确实发到了假服务端（证明连接真的建立起来了）。
	select {
	case raw := <-gotSub:
		if !strings.Contains(raw, "the-secret") {
			t.Fatalf("订阅没有带对凭据：%s", raw)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("超时：没有收到订阅")
	}

	m.Stop(wid)
	if m.IsRunning(wid) {
		t.Fatal("停止后不应再报告运行中")
	}
	// 停止后可以再次启动（run 已从表里移除）。
	if err := m.Start(t.Context(), wid, uid); err != nil {
		t.Fatalf("停止后再次启动失败：%v", err)
	}
	m.Stop(wid)
}
