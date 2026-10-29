package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// MailConfig 是「邮箱账单导入」的落库形态。凭据边界与助手配置一致：
// 邮箱地址 + IMAP 授权码以 AES-256-GCM 密文（主密钥 TODO_CONFIG_KEY）存在
// AuthCodeEnc 里，应用内任何视图都不回显明文；auth_code_enc 空串 = 未配置
// 或已被显式撤销。
type MailConfig struct {
	WorkspaceID  int64
	Enabled      bool
	Mailbox      string // 邮箱地址（用户可编辑，回显为非机密标识）
	AuthCodeEnc  string // 授权码密文；空串 = 未配置/已撤销
	Senders      string // 账单来源发件人，| 分隔（地址或域名子串）；空 = 未配置，窗口内全部交给 Kimi 判定
	LastRunAt    string
	LastRunOk    *bool // nil = 未跑过
	LastRunError string
	LastImported int64
	Version      int64
	UpdatedAt    string
}

// MailConfigInput 是写入意图。两条语义并存（与助手密钥一致）：
//   - 留空不修改：Mailbox / AuthCodeEnc 空串表示保留原值；
//   - 显式清除：ClearMailbox / ClearAuthCode 删除已存值（删除不需要主密钥）。
type MailConfigInput struct {
	Enabled       bool
	Mailbox       string
	ClearMailbox  bool
	AuthCodeEnc   string // 密文；由 API 层用 secretbox 加密后传入
	ClearAuthCode bool
	// Senders 非机密配置：每次提交完整清单、整体写入，
	// 没有「留空保留」语义（清空即不设来源，窗口内全部交给 Kimi 判定）。
	Senders string
}

const (
	maxMailboxLen      = 200
	maxMailSenders     = 30
	maxMailSenderRunes = 100
	maxMailSendersLen  = 2000
)

// validatePipeList 校验 | 分隔清单：总长 ≤maxLen 字符、去空条目 ≤maxItems 条、
// 单条 ≤itemRunes 字。
func validatePipeList(raw string, maxItems, itemRunes, maxLen int) error {
	kw := strings.TrimSpace(raw)
	if len(kw) > maxLen {
		return fmt.Errorf("%w: 清单过长", ErrInvalid)
	}
	n := 0
	for _, part := range strings.Split(kw, "|") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		n++
		if len([]rune(strings.TrimSpace(part))) > itemRunes {
			return fmt.Errorf("%w: 单个条目不超过 %d 字", ErrInvalid, itemRunes)
		}
	}
	if n > maxItems {
		return fmt.Errorf("%w: 清单最多 %d 条", ErrInvalid, maxItems)
	}
	return nil
}

func validateMailInput(in MailConfigInput) error {
	if len([]rune(in.Mailbox)) > maxMailboxLen {
		return fmt.Errorf("%w: 邮箱地址过长", ErrInvalid)
	}
	if err := validatePipeList(in.Senders, maxMailSenders, maxMailSenderRunes, maxMailSendersLen); err != nil {
		return err
	}
	return nil
}

const mailCols = `workspace_id, enabled, mailbox, auth_code_enc, senders,
	last_run_at, last_run_ok, last_run_error, last_imported, version, updated_at`

func scanMail(row interface{ Scan(...any) error }) (*MailConfig, error) {
	var c MailConfig
	var lastRunAt sql.NullString
	var lastRunOk sql.NullBool
	err := row.Scan(&c.WorkspaceID, &c.Enabled, &c.Mailbox, &c.AuthCodeEnc, &c.Senders,
		&lastRunAt, &lastRunOk, &c.LastRunError, &c.LastImported, &c.Version, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.LastRunAt = lastRunAt.String
	if lastRunOk.Valid {
		c.LastRunOk = &lastRunOk.Bool
	}
	return &c, nil
}

// GetMailConfig 读取配置，未配置时返回 ErrNotFound。
func (s *Store) GetMailConfig(ctx context.Context, wsID int64) (*MailConfig, error) {
	return scanMail(s.db.QueryRowContext(ctx,
		`SELECT `+mailCols+` FROM workspace_mail_config WHERE workspace_id = ?`, wsID))
}

// SaveMailConfig 写入配置。version 语义与助手配置一致：0 表示允许新建，
// 非 0 时必须与库里一致，否则 ErrConfigConflict。
func (s *Store) SaveMailConfig(ctx context.Context, wsID, userID int64, in MailConfigInput, version int64) (*MailConfig, error) {
	if err := validateMailInput(in); err != nil {
		return nil, err
	}
	var out *MailConfig
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		now := formatTime(nowUTC())

		var existing *MailConfig
		cur, err := scanMail(tx.QueryRowContext(ctx,
			`SELECT `+mailCols+` FROM workspace_mail_config WHERE workspace_id = ?`, wsID))
		switch {
		case err == nil:
			existing = cur
		case errors.Is(err, ErrNotFound):
		default:
			return err
		}

		mailbox := strings.TrimSpace(in.Mailbox)
		switch {
		case in.ClearMailbox:
			mailbox = ""
		case mailbox == "" && existing != nil:
			mailbox = existing.Mailbox // 留空 = 保留原值
		}

		authCodeEnc := in.AuthCodeEnc
		switch {
		case in.ClearAuthCode:
			authCodeEnc = ""
		case authCodeEnc == "" && existing != nil:
			authCodeEnc = existing.AuthCodeEnc // 留空 = 保留原值
		}

		if existing == nil {
			if version != 0 {
				return ErrConfigConflict
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO workspace_mail_config
				   (workspace_id, enabled, mailbox, auth_code_enc, senders, version, updated_by, updated_at)
				 VALUES (?, ?, ?, ?, ?, 1, ?, ?)`,
				wsID, in.Enabled, mailbox, authCodeEnc, strings.TrimSpace(in.Senders), userID, now); err != nil {
				return err
			}
		} else {
			if version != 0 && version != existing.Version {
				return ErrConfigConflict
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE workspace_mail_config SET
				   enabled = ?, mailbox = ?, auth_code_enc = ?, senders = ?, updated_by = ?, updated_at = ?, version = version + 1
				 WHERE workspace_id = ?`,
				in.Enabled, mailbox, authCodeEnc, strings.TrimSpace(in.Senders), userID, now, wsID); err != nil {
				return err
			}
		}

		got, err := scanMail(tx.QueryRowContext(ctx,
			`SELECT `+mailCols+` FROM workspace_mail_config WHERE workspace_id = ?`, wsID))
		if err != nil {
			return err
		}
		out = got
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("保存邮箱导入配置失败：%w", err)
	}
	return out, nil
}

// SetMailEnabled 更新启用意图（幂等开关位，不参与 version 乐观并发，
// 与 SetAssistantEnabled 同理由）。
func (s *Store) SetMailEnabled(ctx context.Context, wsID int64, enabled bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE workspace_mail_config SET enabled = ?, updated_at = ? WHERE workspace_id = ?`,
		enabled, formatTime(nowUTC()), wsID)
	return err
}

// EnabledMailWorkspaceIDs 返回所有「邮箱导入已启用」的工作区，供进程启动时恢复。
func (s *Store) EnabledMailWorkspaceIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT workspace_id FROM workspace_mail_config WHERE enabled = 1 ORDER BY workspace_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// MailSeen 报告该邮件是否已处理过（已落账或已判定非账单）。
func (s *Store) MailSeen(ctx context.Context, wsID int64, messageID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM mail_seen WHERE workspace_id = ? AND message_id = ?`,
		wsID, messageID).Scan(&n)
	return n > 0, err
}

// MarkMailSeen 记录一封邮件已处理。entryID 为 0 表示非账单（不关联账目）。
// 幂等：重复标记同一邮件是 no-op。
func (s *Store) MarkMailSeen(ctx context.Context, wsID int64, messageID string, entryID int64) error {
	var entryArg any
	if entryID > 0 {
		entryArg = entryID
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO mail_seen (workspace_id, message_id, entry_id, created_at)
		 VALUES (?, ?, ?, ?)`,
		wsID, messageID, entryArg, formatTime(nowUTC()))
	return err
}

// RecordMailRun 更新最近一次导入运行的状态（时间、成败、错误、导入笔数）。
// 这是自动导入的「如实呈现」落点：失败原因原样留给设置页展示。
func (s *Store) RecordMailRun(ctx context.Context, wsID int64, ok bool, errMsg string, imported int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE workspace_mail_config SET
		   last_run_at = ?, last_run_ok = ?, last_run_error = ?, last_imported = ?
		 WHERE workspace_id = ?`,
		formatTime(nowUTC()), ok, errMsg, imported, wsID)
	return err
}
