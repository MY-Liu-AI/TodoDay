package api

import (
	"net/http"
	"strconv"
	"strings"

	"tododay/internal/store"
)

// parseLedgerInput validates fields shared by create/update. 支出必须带分类，
// 收入不占分类（category 忽略）；金额以分传入，必须是正整数。
func parseLedgerInput(w http.ResponseWriter, in struct {
	Date      string `json:"date"`
	Type      string `json:"type"`
	Category  string `json:"category"`
	AmountFen int64  `json:"amountFen"`
	Note      string `json:"note"`
}) (store.LedgerInput, bool) {
	if !validDate(in.Date) {
		writeError(w, http.StatusBadRequest, "validation_failed", "日期需为 YYYY-MM-DD")
		return store.LedgerInput{}, false
	}
	typ := in.Type
	if typ != "exp" && typ != "inc" {
		writeError(w, http.StatusBadRequest, "validation_failed", "类型需为 exp 或 inc")
		return store.LedgerInput{}, false
	}
	category := strings.TrimSpace(in.Category)
	if typ == "exp" && category == "" {
		writeError(w, http.StatusBadRequest, "validation_failed", "支出需要分类")
		return store.LedgerInput{}, false
	}
	if len([]rune(category)) > 40 {
		writeError(w, http.StatusBadRequest, "validation_failed", "分类过长")
		return store.LedgerInput{}, false
	}
	if in.AmountFen < 1 {
		writeError(w, http.StatusBadRequest, "validation_failed", "金额需大于 0")
		return store.LedgerInput{}, false
	}
	note := strings.TrimSpace(in.Note)
	if len([]rune(note)) > 240 {
		writeError(w, http.StatusBadRequest, "validation_failed", "备注过长")
		return store.LedgerInput{}, false
	}
	return store.LedgerInput{
		Date: in.Date, Type: typ, Category: category,
		AmountFen: in.AmountFen, Note: note,
	}, true
}

type ledgerPayload struct {
	Date      string `json:"date"`
	Type      string `json:"type"`
	Category  string `json:"category"`
	AmountFen int64  `json:"amountFen"`
	Note      string `json:"note"`
}

func (s *Server) listLedger(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	month := r.URL.Query().Get("month")
	if len(month) != 7 || month[4] != '-' {
		writeError(w, http.StatusBadRequest, "validation_failed", "month 需为 YYYY-MM")
		return
	}
	if _, err := strconv.Atoi(month[:4]); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "month 需为 YYYY-MM")
		return
	}
	if m, err := strconv.Atoi(month[5:]); err != nil || m < 1 || m > 12 {
		writeError(w, http.StatusBadRequest, "validation_failed", "month 需为 YYYY-MM")
		return
	}
	entries, stats, err := s.st.ListLedger(r.Context(), wid, month)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "stats": stats})
}

func (s *Server) createLedgerEntry(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	var in struct {
		ledgerPayload
		ClientID string `json:"clientId"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	li, ok := parseLedgerInput(w, in.ledgerPayload)
	if !ok {
		return
	}
	if len(in.ClientID) > 64 {
		writeError(w, http.StatusBadRequest, "validation_failed", "clientId 过长")
		return
	}
	li.ClientID = in.ClientID
	e, err := s.st.CreateLedgerEntry(r.Context(), wid, u.ID, li)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

func (s *Server) updateLedgerEntry(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	var in struct {
		ledgerPayload
		Version int64 `json:"version"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	li, ok := parseLedgerInput(w, in.ledgerPayload)
	if !ok {
		return
	}
	eid, ok := parseEID(w, r)
	if !ok {
		return
	}
	e, err := s.st.UpdateLedgerEntry(r.Context(), wid, eid, li, in.Version)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) deleteLedgerEntries(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	ids, ok := readLedgerIDs(w, r)
	if !ok {
		return
	}
	if err := s.st.DeleteLedgerEntries(r.Context(), wid, ids); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": ids, "restorable": true})
}

func (s *Server) restoreLedgerEntries(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	ids, ok := readLedgerIDs(w, r)
	if !ok {
		return
	}
	entries, err := s.st.RestoreLedgerEntries(r.Context(), wid, ids)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// readLedgerIDs decodes a batch of numeric ledger entry ids（1–500）。
func readLedgerIDs(w http.ResponseWriter, r *http.Request) ([]int64, bool) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := decodeJSON(r, &in); err != nil || len(in.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "validation_failed", "ids 不能为空")
		return nil, false
	}
	if len(in.IDs) > maxBatch {
		writeError(w, http.StatusBadRequest, "validation_failed", "单次最多 500 项")
		return nil, false
	}
	return in.IDs, true
}

// ── 分类预算：每个分类一个长期月度预算（设置一次、每月沿用直到修改）。
// 考核口径（当月该分类支出）由前端从当月流水推导，与三卡汇总同口径；
// 这里只存预算行。写操作响应都回全量列表——客户端直接替换，不做合并。

func (s *Server) listLedgerBudgets(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	budgets, err := s.st.ListLedgerBudgets(r.Context(), wid)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"budgets": budgets})
}

func (s *Server) putLedgerBudget(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	var in struct {
		Category  string `json:"category"`
		AmountFen int64  `json:"amountFen"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	if _, err := s.st.UpsertLedgerBudget(r.Context(), wid, in.Category, in.AmountFen); err != nil {
		mapStoreError(w, err)
		return
	}
	s.listLedgerBudgets(w, r, u, wid)
}

func (s *Server) deleteLedgerBudget(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	category := r.PathValue("category")
	if err := s.st.DeleteLedgerBudget(r.Context(), wid, category); err != nil {
		mapStoreError(w, err)
		return
	}
	s.listLedgerBudgets(w, r, u, wid)
}

func parseEID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	eid, err := strconv.ParseInt(r.PathValue("eid"), 10, 64)
	if err != nil || eid <= 0 {
		writeError(w, http.StatusBadRequest, "validation_failed", "无效的账目 ID")
		return 0, false
	}
	return eid, true
}
