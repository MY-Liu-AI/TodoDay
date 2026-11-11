package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// LedgerCategory 是一个工作区的记账分类。分类此前是纯自由文本
//（ledger_entries/ledger_budgets 里的 category 列，兼容旧数据不变）；
// 本表让分类可管理：用户在网页增/改名/删，邮箱账单解析（Kimi）按
// 这份工作区清单收敛——模型给出的分类不在清单内一律归「其他」。
// 与账目/预算同边界：成员级，工作区内共享一份。
type LedgerCategory struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	Position  int    `json:"position"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// 默认六分类（用户指定，2026-10-29）：色板沿用日历六色编码
//（AGENTS.md：分类色是数据编码，允许六色并存，主操作仍只有青绿一色）。
// 与前端 lib/ledger.js 的 LEDGER_CATS 保持一致。
var defaultLedgerCategories = []struct {
	Name  string
	Color string
}{
	{"餐饮", "peach"},
	{"交通", "slate"},
	{"购物", "coral"},
	{"住房", "teal"},
	{"娱乐", "cyan"},
	{"其他", "yellow"},
}

// 新增分类的自动取色顺序：按 position 轮转六色板。与默认清单同色序，
// 保证默认六类恰好各占一色；超过六类时重复用色——身份由文字标签承担
//（柱图/胶囊都直标分类名），颜色只是辅助编码。
var ledgerCategoryPalette = []string{"peach", "slate", "coral", "teal", "cyan", "yellow"}

func validateLedgerCategoryName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: 分类名不能为空", ErrInvalid)
	}
	if len([]rune(name)) > 40 {
		return fmt.Errorf("%w: 分类名过长", ErrInvalid)
	}
	return nil
}

// CategoryInUseError 删除分类被在用流水（含软删）挡住时返回，Count 是
// 占用笔数——API 层据此给出「先迁移账目」的明确提示。
type CategoryInUseError struct{ Count int64 }

func (e *CategoryInUseError) Error() string { return fmt.Sprintf("category in use: %d entries", e.Count) }

func scanLedgerCategory(row interface{ Scan(...any) error }) (*LedgerCategory, error) {
	var c LedgerCategory
	err := row.Scan(&c.ID, &c.Name, &c.Color, &c.Position, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

const ledgerCategoryCols = `SELECT id, name, color, position, created_at, updated_at
	FROM ledger_categories WHERE workspace_id = ? ORDER BY position, id`

// 单行查询单独一条：不能往带 ORDER BY 的 ledgerCategoryCols 后面拼
// AND——SQLite 会把 「id AND id = ?」当成排序表达式，静默返回错行。
const ledgerCategoryByID = `SELECT id, name, color, position, created_at, updated_at
	FROM ledger_categories WHERE workspace_id = ? AND id = ?`

// ListLedgerCategories 返回工作区分类清单（position 升序）。
// 首次访问（一行没有）时在一个事务里播种默认六分类——存量工作区
// 因此无需数据迁移；旧流水里的历史分类名（如「通勤」「交通」旧写法）
// 不受影响，仍按自由文本展示（色板 slate 兜底）。
func (s *Store) ListLedgerCategories(ctx context.Context, wsID int64) ([]LedgerCategory, error) {
	list, err := s.queryLedgerCategories(ctx, wsID)
	if err != nil {
		return nil, err
	}
	if len(list) > 0 {
		return list, nil
	}
	now := formatTime(nowUTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for i, d := range defaultLedgerCategories {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO ledger_categories (workspace_id, name, color, position, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			wsID, d.Name, d.Color, i, now, now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.queryLedgerCategories(ctx, wsID)
}

func (s *Store) queryLedgerCategories(ctx context.Context, wsID int64) ([]LedgerCategory, error) {
	rows, err := s.db.QueryContext(ctx, ledgerCategoryCols, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LedgerCategory{}
	for rows.Next() {
		c, err := scanLedgerCategory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// AddLedgerCategory 追加一个分类：position 排到末尾，颜色按色板轮转
// 自动分配。重名返回 ErrInvalid（UNIQUE(workspace_id, name) 兜底）——
// 重名是可预见的输入问题，语义上是校验错而非并发冲突。
func (s *Store) AddLedgerCategory(ctx context.Context, wsID int64, name string) (*LedgerCategory, error) {
	name = strings.TrimSpace(name)
	if err := validateLedgerCategoryName(name); err != nil {
		return nil, err
	}
	// 先取落位（MAX(position)+1），颜色按落位轮转六色板。
	var pos int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(position) + 1, 0) FROM ledger_categories WHERE workspace_id = ?`, wsID).Scan(&pos); err != nil {
		return nil, err
	}
	now := formatTime(nowUTC())
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO ledger_categories (workspace_id, name, color, position, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		wsID, name, ledgerCategoryPalette[pos%len(ledgerCategoryPalette)], pos, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			// 重名是可预见的输入问题（客户端可先行查重），按校验错处理。
			return nil, fmt.Errorf("%w: 分类已存在", ErrInvalid)
		}
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return scanLedgerCategory(s.db.QueryRowContext(ctx, ledgerCategoryByID, wsID, id))
}

// RenameLedgerCategory 给分类改名，并在同一事务里级联账目与预算：
// 账本里该分类的全部流水（含软删，恢复后仍指新名）与对应预算行一起
// 改名；流水 version +1（与目标同约定：改的是底层数据，正在编辑的
// 客户端会收到 409，重拉对齐）。重名返回 ErrConflict。
func (s *Store) RenameLedgerCategory(ctx context.Context, wsID, id int64, newName string) (*LedgerCategory, error) {
	newName = strings.TrimSpace(newName)
	if err := validateLedgerCategoryName(newName); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	c, err := scanLedgerCategory(tx.QueryRowContext(ctx, ledgerCategoryByID, wsID, id))
	if err != nil {
		return nil, err
	}
	if c.Name == newName {
		return c, tx.Commit()
	}
	now := formatTime(nowUTC())
	if _, err := tx.ExecContext(ctx,
		`UPDATE ledger_categories SET name = ?, updated_at = ? WHERE id = ?`, newName, now, id); err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w: 分类已存在", ErrInvalid)
		}
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE ledger_entries SET category = ?, version = version + 1
		 WHERE workspace_id = ? AND category = ?`, newName, wsID, c.Name); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE ledger_budgets SET category = ?, updated_at = ?
		 WHERE workspace_id = ? AND category = ?`, newName, now, wsID, c.Name); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return scanLedgerCategory(s.db.QueryRowContext(ctx, ledgerCategoryByID, wsID, id))
}

// DeleteLedgerCategory 删除一个分类。保护：该分类下还有流水（含软删）
// 时不允许删除——ErrConflict，前端提示先迁移账目；无流水则连它的预算
// 行一并清掉（分类没了，预算失去意义）。
func (s *Store) DeleteLedgerCategory(ctx context.Context, wsID, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c, err := scanLedgerCategory(tx.QueryRowContext(ctx, ledgerCategoryByID, wsID, id))
	if err != nil {
		return err
	}
	var n int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM ledger_entries WHERE workspace_id = ? AND category = ?`, wsID, c.Name).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return &CategoryInUseError{Count: n}
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM ledger_budgets WHERE workspace_id = ? AND category = ?`, wsID, c.Name); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM ledger_categories WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
