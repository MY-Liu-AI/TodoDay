package aibot

import (
	"context"
	"errors"
	"testing"
	"time"
)

// 「不该连接」的两种情况必须与「暂时取不到」严格区分。
//
// 混为一谈就会出安全问题：把「凭据已被撤销」当成网络抖动，助手会带着
// 一份用户以为已经删掉的凭据继续运行。这组测试固定住这个边界。

// 停用时**根本不该拨号**。
//
// 故意用必然连不上的地址：若实现真去连接，返回的会是握手/拨号错误；
// 只有「压根没连」才会原样返回 ErrDisabled。这样测的是行为本身。
func TestDisabledSourcePreventsDial(t *testing.T) {
	c := NewClient("", "", "ws://127.0.0.1:1/ws", nil)
	c.SetCredentialSource(func(context.Context) (string, string, error) {
		return "", "", ErrDisabled
	})

	err := c.session(context.Background())
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("停用时应返回 ErrDisabled 而不是尝试连接，得到 %v", err)
	}
}

// 凭据被撤销（后端说没有凭据）同样不该拨号，也不该拿旧凭据顶上。
func TestRevokedCredentialsPreventDial(t *testing.T) {
	c := NewClient("stale-bot", "stale-secret", "ws://127.0.0.1:1/ws", nil)
	c.SetCredentialSource(func(context.Context) (string, string, error) {
		return "", "", ErrNoCredentials
	})

	err := c.session(context.Background())
	if !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("凭据撤销时应返回 ErrNoCredentials，得到 %v", err)
	}
}

// 来源没报错却给了空值：也要按「没有凭据」处理，不能静默沿用旧值。
func TestEmptyCredentialsFromSourceStopsConnecting(t *testing.T) {
	c := NewClient("stale-bot", "stale-secret", "ws://127.0.0.1:1/ws", nil)
	c.SetCredentialSource(func(context.Context) (string, string, error) {
		return "", "", nil // 没有报错，但值为空
	})
	if err := c.refreshCredentials(context.Background()); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("空凭据应按未配置处理，得到 %v", err)
	}
}

// 停用不能把已有凭据清掉：用户只是暂时关掉，重新启用后应立刻可连。
func TestDisabledKeepsExistingCredentials(t *testing.T) {
	c := NewClient("keep-bot", "keep-secret", "ws://127.0.0.1:1/ws", nil)
	c.SetCredentialSource(func(context.Context) (string, string, error) {
		return "", "", ErrDisabled
	})

	if err := c.refreshCredentials(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatal("停用时 refreshCredentials 应返回 ErrDisabled")
	}
	id, secret := c.currentCredentials()
	if id != "keep-bot" || secret != "keep-secret" {
		t.Fatalf("停用不应清空已有凭据，得到 %q / %q", id, secret)
	}
}

// 与上面相反：普通取凭据失败必须继续连接，否则一次网络抖动就会让助手静默掉线。
func TestTransientErrorKeepsConnecting(t *testing.T) {
	c := NewClient("b", "s", "ws://127.0.0.1:1/ws", nil)
	c.SetCredentialSource(func(context.Context) (string, string, error) {
		return "", "", errors.New("暂时取不到配置")
	})
	if err := c.refreshCredentials(context.Background()); err != nil {
		t.Fatalf("普通错误不应阻断连接，得到 %v", err)
	}
}

// 没有凭据来源时（用构造时传入的静态凭据）应当继续连接。
func TestNoSourceKeepsConnecting(t *testing.T) {
	c := NewClient("b", "s", "ws://127.0.0.1:1/ws", nil)
	if err := c.refreshCredentials(context.Background()); err != nil {
		t.Fatalf("没有动态来源时应继续连接，得到 %v", err)
	}
}

// 停用后重新启用：来源改变返回值，下次刷新应恢复连接并采用新凭据。
func TestReenableRestoresConnection(t *testing.T) {
	c := NewClient("old-bot", "old-secret", "ws://127.0.0.1:1/ws", nil)
	disabled := true
	c.SetCredentialSource(func(context.Context) (string, string, error) {
		if disabled {
			return "", "", ErrDisabled
		}
		return "new-bot", "new-secret", nil
	})

	if err := c.refreshCredentials(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatal("停用时应返回 ErrDisabled")
	}
	disabled = false
	if err := c.refreshCredentials(context.Background()); err != nil {
		t.Fatalf("重新启用后应恢复连接，得到 %v", err)
	}
	id, secret := c.currentCredentials()
	if id != "new-bot" || secret != "new-secret" {
		t.Fatalf("重新启用后应采用新凭据，得到 %q / %q", id, secret)
	}
}

// 撤销后重新配置：应从「没有凭据」恢复为可连接。
func TestReconfigureAfterRevocation(t *testing.T) {
	c := NewClient("old-bot", "old-secret", "ws://127.0.0.1:1/ws", nil)
	revoked := true
	c.SetCredentialSource(func(context.Context) (string, string, error) {
		if revoked {
			return "", "", ErrNoCredentials
		}
		return "fresh-bot", "fresh-secret", nil
	})

	if err := c.refreshCredentials(context.Background()); !errors.Is(err, ErrNoCredentials) {
		t.Fatal("撤销时应返回 ErrNoCredentials")
	}
	revoked = false
	if err := c.refreshCredentials(context.Background()); err != nil {
		t.Fatalf("重新配置后应可连接，得到 %v", err)
	}
	if id, _ := c.currentCredentials(); id != "fresh-bot" {
		t.Fatalf("应采用新凭据，得到 %q", id)
	}
}

// 「不该连接」期间 Run 必须保持轮询而不是退出：退出就意味着用户必须重启
// 进程，那等于把「配置开关」变成了事实上的进程管理。
func TestRunKeepsPollingWhileNotConnectable(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"停用", ErrDisabled},
		{"无凭据", ErrNoCredentials},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := NewClient("", "", "ws://127.0.0.1:1/ws", nil)
			var calls int
			c.SetCredentialSource(func(context.Context) (string, string, error) {
				calls++
				return "", "", tc.err
			})

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- c.Run(ctx) }()

			// 轮询间隔是 backoffMin(3s)，等它至少检查两轮。
			time.Sleep(backoffMin + 500*time.Millisecond)
			cancel()

			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("ctx 取消后 Run 应返回 context.Canceled，得到 %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Run 在 ctx 取消后没有退出")
			}

			if calls < 2 {
				t.Fatalf("应持续轮询（至少 2 次检查），实际 %d 次", calls)
			}
		})
	}
}
