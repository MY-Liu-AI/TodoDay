package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// LedgerEntry 是一笔收入或支出。Date 是 YYYY-MM-DD 本地日历日期（客户端
// 语义，与 tasks.due_date 同约定）；金额以分存储。Category 是自由文本、
// 服务端不解释（与 goals.group_name 同约定），收入不占分类（Category = ""）。
type LedgerEntry struct {
	ID        int64  `json:"id"`
	Date      string `json:"date"` // YYYY-MM-DD
	Type      string `json:"type"` // exp | inc
	Category  string `json:"category"`
	AmountFen int64  `json:"amountFen"`
	Note      string `json:"note"`
	Version   int64  `json:"version"`
	CreatedAt string `json:"createdAt"`
}

// LedgerInput carries validated fields for create/update.
type LedgerInput struct {
	Date      string // YYYY-MM-DD
	Type      string // exp | inc
	Category  string // 仅支出使用；收入传 ""
	AmountFen int64
	Note      string
	ClientID  string // 仅创建使用，幂等键
}

// LedgerMonthStats 是单月收支汇总，与列表同一查询口径（只计未删除）。
type LedgerMonthStats struct {
	Exp int64 `json:"exp"`
	Inc int64 `json:"inc"`
	Net int64 `json:"net"`
}

const maxLedgerAmountFen = int64(1_000_000_000_000) // ¥100 亿，防溢出与脏数据

func validateLedgerInput(in LedgerInput) error {
	if in.Type != "exp" && in.Type != "inc" {
		return fmt.Errorf("%w: type 无效", ErrInvalid)
	}
	if in.AmountFen < 1 || in.AmountFen > maxLedgerAmountFen {
		return fmt.Errorf("%w: 金额需在 0.01–100 亿元之间", ErrInvalid)
	}
	if in.Type == "exp" && strings.TrimSpace(in.Category) == "" {
		return fmt.Errorf("%w: 支出需要分类", ErrInvalid)
	}
	if len([]rune(in.Category)) > 40 {
		return fmt.Errorf("%w: 分类过长", ErrInvalid)
	}
	if len([]rune(in.Note)) > 240 {
		return fmt.Errorf("%w: 备注过长", ErrInvalid)
	}
	if len(in.ClientID) > 64 {
		return fmt.Errorf("%w: clientId 过长", ErrInvalid)
	}
	return nil
}

const ledgerCols = `id, entry_date, type, COALESCE(category, ''), amount_fen, note, version, created_at`

func scanLedgerEntry(row interface{ Scan(...any) error }) (*LedgerEntry, error) {
	var e LedgerEntry
	err := row.Scan(&e.ID, &e.Date, &e.Type, &e.Category, &e.AmountFen, &e.Note, &e.Version, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func ledgerByClientTx(ctx context.Context, tx *sql.Tx, wsID int64, clientID string) (*LedgerEntry, error) {
	return scanLedgerEntry(tx.QueryRowContext(ctx,
		`SELECT `+ledgerCols+` FROM ledger_entries WHERE workspace_id = ? AND client_id = ?`, wsID, clientID))
}

func loadLedgerEntryTx(ctx context.Context, tx *sql.Tx, wsID, id int64) (*LedgerEntry, error) {
	return scanLedgerEntry(tx.QueryRowContext(ctx,
		`SELECT `+ledgerCols+` FROM ledger_entries WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL`, id, wsID))
}

// CreateLedgerEntry 记一笔。clientID 使创建幂等：响应丢失后重试返回原行，
// 不产生重复账目（重放不区分 soft-deleted，已删行也原样返回）。
func (s *Store) CreateLedgerEntry(ctx context.Context, wsID, userID int64, in LedgerInput) (*LedgerEntry, error) {
	if err := validateLedgerInput(in); err != nil {
		return nil, err
	}
	now := formatTime(nowUTC())
	var created *LedgerEntry
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		// Idempotent replay.
		if in.ClientID != "" {
			existing, err := ledgerByClientTx(ctx, tx, wsID, in.ClientID)
			if err == nil {
				created = existing
				return nil
			}
			if !errors.Is(err, ErrNotFound) {
				return err
			}
		}
		category := strings.TrimSpace(in.Category)
		var categoryArg any
		if in.Type == "exp" {
			categoryArg = category
		}
		var clientArg any
		if in.ClientID != "" {
			clientArg = in.ClientID
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO ledger_entries
			   (workspace_id, created_by, entry_date, type, category, amount_fen, note, client_id, version, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
			wsID, userID, in.Date, in.Type, categoryArg, in.AmountFen, in.Note, clientArg, now, now)
		if err != nil {
			if isUniqueViolation(err) && in.ClientID != "" {
				existing, err2 := ledgerByClientTx(ctx, tx, wsID, in.ClientID)
				if err2 != nil {
					return err2
				}
				created = existing
				return nil
			}
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		e, err := loadLedgerEntryTx(ctx, tx, wsID, id)
		if err != nil {
			return err
		}
		created = e
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// UpdateLedgerEntry 应用乐观并发保护的编辑。version 不匹配返回
// ErrConflict（与目标同约定：409 只带提示，客户端重拉当月数据即可）。
func (s *Store) UpdateLedgerEntry(ctx context.Context, wsID, id int64, in LedgerInput, version int64) (*LedgerEntry, error) {
	if err := validateLedgerInput(in); err != nil {
		return nil, err
	}
	now := formatTime(nowUTC())
	var out *LedgerEntry
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		category := strings.TrimSpace(in.Category)
		var categoryArg any
		if in.Type == "exp" {
			categoryArg = category
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE ledger_entries SET entry_date = ?, type = ?, category = ?, amount_fen = ?, note = ?, version = version + 1, updated_at = ?
			 WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL AND version = ?`,
			in.Date, in.Type, categoryArg, in.AmountFen, in.Note, now, id, wsID, version)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			// 区分「不存在」与「版本冲突」：存在但版本不对 → conflict。
			var exists int
			if err := tx.QueryRowContext(ctx,
				`SELECT COUNT(1) FROM ledger_entries WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL`, id, wsID).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				return ErrNotFound
			}
			return ErrConflict
		}
		e, err := loadLedgerEntryTx(ctx, tx, wsID, id)
		if err != nil {
			return err
		}
		out = e
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteLedgerEntries 批量软删（单事务，任一 id 不存在则整体回滚）。
func (s *Store) DeleteLedgerEntries(ctx context.Context, wsID int64, ids []int64) error {
	now := formatTime(nowUTC())
	return s.ExecTx(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			res, err := tx.ExecContext(ctx,
				`UPDATE ledger_entries SET deleted_at = ?, updated_at = ? WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL`,
				now, now, id, wsID)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n == 0 {
				return ErrNotFound
			}
		}
		return nil
	})
}

// RestoreLedgerEntries 撤销删除，返回恢复后的完整账目（供前端直接放回列表）。
func (s *Store) RestoreLedgerEntries(ctx context.Context, wsID int64, ids []int64) ([]LedgerEntry, error) {
	now := formatTime(nowUTC())
	out := []LedgerEntry{}
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			res, err := tx.ExecContext(ctx,
				`UPDATE ledger_entries SET deleted_at = NULL, version = version + 1, updated_at = ? WHERE id = ? AND workspace_id = ?`,
				now, id, wsID)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n == 0 {
				return ErrNotFound
			}
			e, err := loadLedgerEntryTx(ctx, tx, wsID, id)
			if err != nil {
				return err
			}
			out = append(out, *e)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListLedger 返回某月全部未删账目（entry_date 倒序、同日内新记的在前）
// 与同一口径的月汇总。month 是固定宽度 YYYY-MM，前缀匹配即正确。
func (s *Store) ListLedger(ctx context.Context, wsID int64, month string) ([]LedgerEntry, *LedgerMonthStats, error) {
	var entries []LedgerEntry
	stats := &LedgerMonthStats{}
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT `+ledgerCols+` FROM ledger_entries
			 WHERE workspace_id = ? AND substr(entry_date, 1, 7) = ? AND deleted_at IS NULL
			 ORDER BY entry_date DESC, id DESC`, wsID, month)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanLedgerEntry(rows)
			if err != nil {
				return err
			}
			entries = append(entries, *e)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		var exp, inc sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT
			   SUM(CASE WHEN type = 'exp' THEN amount_fen END),
			   SUM(CASE WHEN type = 'inc' THEN amount_fen END)
			 FROM ledger_entries
			 WHERE workspace_id = ? AND substr(entry_date, 1, 7) = ? AND deleted_at IS NULL`, wsID, month).
			Scan(&exp, &inc); err != nil {
			return err
		}
		if exp.Valid {
			stats.Exp = exp.Int64
		}
		if inc.Valid {
			stats.Inc = inc.Int64
		}
		stats.Net = stats.Inc - stats.Exp
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if entries == nil {
		entries = []LedgerEntry{}
	}
	return entries, stats, nil
}
