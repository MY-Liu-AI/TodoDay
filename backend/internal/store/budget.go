package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// LedgerBudget 是一个分类的长期月度预算：设置一次、每月沿用直到修改。
// 考核口径不在服务端——与三卡汇总同口径，由前端从当月未删除支出推导。
// 与账目同为成员级数据（不设 owner 边界）。分类是自由文本（与账目同约定），
// 每个工作区一个分类至多一条预算。
type LedgerBudget struct {
	ID        int64  `json:"id"`
	Category  string `json:"category"`
	AmountFen int64  `json:"amountFen"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

func validateLedgerBudget(category string, amountFen int64) error {
	if strings.TrimSpace(category) == "" {
		return fmt.Errorf("%w: 分类不能为空", ErrInvalid)
	}
	if len([]rune(category)) > 40 {
		return fmt.Errorf("%w: 分类过长", ErrInvalid)
	}
	if amountFen < 1 || amountFen > maxLedgerAmountFen {
		return fmt.Errorf("%w: 预算需在 0.01–100 亿元之间", ErrInvalid)
	}
	return nil
}

func scanLedgerBudget(row interface{ Scan(...any) error }) (*LedgerBudget, error) {
	var b LedgerBudget
	err := row.Scan(&b.ID, &b.Category, &b.AmountFen, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ListLedgerBudgets 返回工作区全部分类预算，按插入顺序（id 升序）——
// 展示顺序即用户维护优先级，消费变化不引起行重排。
func (s *Store) ListLedgerBudgets(ctx context.Context, wsID int64) ([]LedgerBudget, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, category, amount_fen, created_at, updated_at
		 FROM ledger_budgets WHERE workspace_id = ? ORDER BY id`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LedgerBudget{}
	for rows.Next() {
		b, err := scanLedgerBudget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// UpsertLedgerBudget 按分类写入预算：不存在则建、存在则改金额（LWW 收敛，
// 两个标签页先后保存同一分类，结果就是后存的那份——预算低风险的合理默认，
// 与账目条目的乐观并发不同：预算没有「基于旧值编辑」的语义）。
// 返回写入后的完整行。
func (s *Store) UpsertLedgerBudget(ctx context.Context, wsID int64, category string, amountFen int64) (*LedgerBudget, error) {
	category = strings.TrimSpace(category)
	if err := validateLedgerBudget(category, amountFen); err != nil {
		return nil, err
	}
	now := formatTime(nowUTC())
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO ledger_budgets (workspace_id, category, amount_fen, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (workspace_id, category)
		 DO UPDATE SET amount_fen = excluded.amount_fen, updated_at = excluded.updated_at`,
		wsID, category, amountFen, now, now); err != nil {
		return nil, err
	}
	// 按 (workspace_id, category) 回读：UPSERT 的 UPDATE 分支不写
	// last_insert_rowid，LastInsertId 不可靠。
	return scanLedgerBudget(s.db.QueryRowContext(ctx,
		`SELECT id, category, amount_fen, created_at, updated_at
		 FROM ledger_budgets WHERE workspace_id = ? AND category = ?`, wsID, category))
}

// DeleteLedgerBudget 按分类删除预算；不存在返回 ErrNotFound。
func (s *Store) DeleteLedgerBudget(ctx context.Context, wsID int64, category string) error {
	category = strings.TrimSpace(category)
	if category == "" {
		return fmt.Errorf("%w: 分类不能为空", ErrInvalid)
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM ledger_budgets WHERE workspace_id = ? AND category = ?`, wsID, category)
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
	return nil
}
