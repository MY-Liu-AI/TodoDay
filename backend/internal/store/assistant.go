package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrConfigConflict 表示助手配置的 version 已过期（有人在别处改过）。
//
// 刻意不复用 ConflictError：那个类型要求 Current *Task，而 mapStoreError
// 会无条件写出 "task" 字段，用在这里会把客户端引向一个不存在的任务。
var ErrConfigConflict = errors.New("config conflict")

// AssistantConfig 是落库形态。两个 *Enc 字段是密文，解密不在这里做 ——
// store 不碰加密，由 API 层负责。
type AssistantConfig struct {
	WorkspaceID   int64
	Enabled       bool
	BotID         string
	BotSecretEnc  string
	KimiAPIKeyEnc string
	KimiBase      string
	KimiModel     string
	Version       int64
	UpdatedAt     string
}

// AssistantConfigInput 是写入意图。
//
// 两个密文字段为空串时表示「保留原值」：前端不回显密钥，所以用户不重新
// 输入就不该把已存的密钥抹掉。
type AssistantConfigInput struct {
	Enabled       bool
	BotID         string
	BotSecretEnc  string
	KimiAPIKeyEnc string
	KimiBase      string
	KimiModel     string
	// 显式清除。空密文只能表示「保留原值」（前端不回显密钥，不能因为
	// 用户没重填就把密钥抹掉），所以要删除已存凭据必须显式说出来 ——
	// 否则「撤销」这个动作在系统里根本不存在。
	ClearBotSecret  bool
	ClearKimiAPIKey bool
}

const assistantCols = `workspace_id, enabled, bot_id, bot_secret_enc, kimi_api_key_enc,
	kimi_base, kimi_model, version, updated_at`

func scanAssistant(row interface{ Scan(...any) error }) (*AssistantConfig, error) {
	var c AssistantConfig
	err := row.Scan(&c.WorkspaceID, &c.Enabled, &c.BotID, &c.BotSecretEnc,
		&c.KimiAPIKeyEnc, &c.KimiBase, &c.KimiModel, &c.Version, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetAssistantConfig 读取配置，未配置时返回 ErrNotFound。
func (s *Store) GetAssistantConfig(ctx context.Context, wsID int64) (*AssistantConfig, error) {
	return scanAssistant(s.db.QueryRowContext(ctx,
		`SELECT `+assistantCols+` FROM workspace_assistant_config WHERE workspace_id = ?`, wsID))
}

// SaveAssistantConfig 写入配置。
//
// version 为 0 表示「允许新建或覆盖当前版本」，非 0 时必须与库里一致，
// 否则返回 ErrConfigConflict —— 与任务的乐观并发语义一致。
func (s *Store) SaveAssistantConfig(ctx context.Context, wsID, userID int64, in AssistantConfigInput, version int64) (*AssistantConfig, error) {
	var out *AssistantConfig
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		now := formatTime(nowUTC())

		var existing *AssistantConfig
		cur, err := scanAssistant(tx.QueryRowContext(ctx,
			`SELECT `+assistantCols+` FROM workspace_assistant_config WHERE workspace_id = ?`, wsID))
		switch {
		case err == nil:
			existing = cur
		case errors.Is(err, ErrNotFound):
			// 尚未配置，走新建。
		default:
			return err
		}

		// 先在 Go 里算出最终要写入的两个密文，规则一目了然：
		//   显式清除   → 空（真删除）
		//   提供了新值 → 新密文
		//   否则       → 保留原值（前端不回显密钥，没重填就不该抹掉）
		// 比在 SQL 里嵌套 CASE 清楚，也不会因为参数顺序写错而误删。
		prevBot, prevKimi := "", ""
		if existing != nil {
			prevBot, prevKimi = existing.BotSecretEnc, existing.KimiAPIKeyEnc
		}
		// 「清除」与「新值」同时给出属于矛盾输入，这里以**清除**为准。
		// 取这个方向是因为两者的失败代价不对称：删掉之后凭据缺失是可见的
		// （助手马上报「没有凭据」），而留着一份用户以为已删的凭据是静默的
		// 危险 —— 正是要堵的 fail-open。所以 switch 的顺序是刻意的，不是碰巧。
		botSecretEnc := prevBot
		switch {
		case in.ClearBotSecret:
			botSecretEnc = ""
		case in.BotSecretEnc != "":
			botSecretEnc = in.BotSecretEnc
		}
		kimiEnc := prevKimi
		switch {
		case in.ClearKimiAPIKey:
			kimiEnc = ""
		case in.KimiAPIKeyEnc != "":
			kimiEnc = in.KimiAPIKeyEnc
		}

		if existing == nil {
			// 首次写入：version 若被指定（客户端以为存在）则视为冲突。
			if version != 0 {
				return ErrConfigConflict
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO workspace_assistant_config
				   (workspace_id, enabled, bot_id, bot_secret_enc, kimi_api_key_enc,
				    kimi_base, kimi_model, updated_by, updated_at, version)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
				wsID, in.Enabled, in.BotID, botSecretEnc, kimiEnc,
				in.KimiBase, in.KimiModel, userID, now); err != nil {
				return err
			}
		} else {
			if version != 0 && version != existing.Version {
				return ErrConfigConflict
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE workspace_assistant_config SET
				   enabled = ?,
				   bot_id = ?,
				   bot_secret_enc = ?,
				   kimi_api_key_enc = ?,
				   kimi_base = ?,
				   kimi_model = ?,
				   updated_by = ?,
				   updated_at = ?,
				   version = version + 1
				 WHERE workspace_id = ?`,
				in.Enabled, in.BotID, botSecretEnc, kimiEnc,
				in.KimiBase, in.KimiModel, userID, now, wsID); err != nil {
				return err
			}
		}

		got, err := scanAssistant(tx.QueryRowContext(ctx,
			`SELECT `+assistantCols+` FROM workspace_assistant_config WHERE workspace_id = ?`, wsID))
		if err != nil {
			return err
		}
		out = got
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("保存助手配置失败：%w", err)
	}
	return out, nil
}
