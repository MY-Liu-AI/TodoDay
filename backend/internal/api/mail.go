package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"tododay/internal/mailimport"
	"tododay/internal/store"
)

// 邮箱账单导入（原生 IMAP）接口，职责与助手接口对齐：
//
//	GET   配置视图（成员可见）—— 不含任何密钥：只有「是否已设置」掩码
//	PUT   保存配置（仅 owner）—— 邮箱地址/授权码/关键词；授权码经 secretbox 加密落库
//	POST  test（仅 owner）       —— 用表单当前值（或已存凭据）做一次 IMAP 登录验证
//	POST  start/stop（仅 owner） —— 启停每日导入循环
//
// 凭据语义与助手密钥一致：授权码留空 = 不修改，删除必须显式 clearAuthCode
// （撤销是删除不是修改，不需要主密钥）；服务端没有主密钥时拒绝存新授权码
// （400 encryption_unavailable），绝不退化成明文。

// mailView 是配置视图。刻意没有任何凭据字段——authCodeSet 只是
// 「是否已设置」的布尔掩码，与助手的 botSecretSet/kimiApiKeySet 同规则。
func mailView(cfg *store.MailConfig, running bool) map[string]any {
	if cfg == nil {
		return map[string]any{
			"configured":   false,
			"enabled":      false,
			"running":      false,
			"mailbox":      "",
			"authCodeSet":  false,
			"senders":      "",
			"lastRunAt":    "",
			"lastRunOk":    nil,
			"lastRunError": "",
			"lastImported": 0,
			"version":      0,
		}
	}
	return map[string]any{
		"configured":   true,
		"enabled":      cfg.Enabled,
		"running":      running,
		"mailbox":      cfg.Mailbox,
		"authCodeSet":  cfg.AuthCodeEnc != "",
		"senders":      cfg.Senders,
		"lastRunAt":    cfg.LastRunAt,
		"lastRunOk":    cfg.LastRunOk,
		"lastRunError": cfg.LastRunError,
		"lastImported": cfg.LastImported,
		"version":      cfg.Version,
	}
}

func (s *Server) getMailConfig(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	cfg, err := s.st.GetMailConfig(r.Context(), wid)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, mailView(nil, false))
		return
	}
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, mailView(cfg, s.mail.IsRunning(wid)))
}

const (
	maxMailAddrLen    = 200
	maxAuthCodeLen    = 200
	maxMailSendersLen = 2000
)

func (s *Server) putMailConfig(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	var in struct {
		Mailbox       string `json:"mailbox"`
		AuthCode      string `json:"authCode"`
		ClearAuthCode bool   `json:"clearAuthCode"`
		Senders       string `json:"senders"`
		Version       int64  `json:"version"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	mailbox := strings.TrimSpace(in.Mailbox)
	if len([]rune(mailbox)) > maxMailAddrLen {
		writeError(w, http.StatusBadRequest, "validation_failed", "邮箱地址过长")
		return
	}
	if len([]rune(strings.TrimSpace(in.AuthCode))) > maxAuthCodeLen {
		writeError(w, http.StatusBadRequest, "validation_failed", "授权码过长")
		return
	}
	if len([]rune(strings.TrimSpace(in.Senders))) > maxMailSendersLen {
		writeError(w, http.StatusBadRequest, "validation_failed", "来源清单过长")
		return
	}

	// 存新授权码必须有主密钥；绝不退化成明文存储。撤销（删除）则不需要。
	if strings.TrimSpace(in.AuthCode) != "" && s.box == nil {
		writeError(w, http.StatusBadRequest, "encryption_unavailable",
			"服务端未配置主密钥，无法保存授权码。请设置环境变量 TODO_CONFIG_KEY（32 字节 base64）后重启服务。")
		return
	}

	// 保留当前 enabled：启停由 start/stop 端点控制，保存配置不改开关。
	enabled := false
	if cur, err := s.st.GetMailConfig(r.Context(), wid); err == nil {
		enabled = cur.Enabled
	} else if !errors.Is(err, store.ErrNotFound) {
		mapStoreError(w, err)
		return
	}

	input := store.MailConfigInput{
		Enabled:       enabled,
		Mailbox:       mailbox,
		ClearAuthCode: in.ClearAuthCode,
		Senders:       strings.TrimSpace(in.Senders),
	}
	if s.box != nil && strings.TrimSpace(in.AuthCode) != "" {
		enc, err := s.box.Seal(strings.TrimSpace(in.AuthCode))
		if err != nil {
			mapStoreError(w, err)
			return
		}
		input.AuthCodeEnc = enc
	}

	cfg, err := s.st.SaveMailConfig(r.Context(), wid, u.ID, input, in.Version)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, mailView(cfg, s.mail.IsRunning(wid)))
}

// testMail 用表单当前值（或已存凭据）做一次真实 IMAP 登录。登录失败是
// 正常结果（HTTP 仍 200），由 ok/message 驱动行内反馈。
func (s *Server) testMail(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	var in struct {
		Mailbox  string `json:"mailbox"`
		AuthCode string `json:"authCode"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	mailbox, err := s.mail.Test(ctx, wid, strings.TrimSpace(in.Mailbox), strings.TrimSpace(in.AuthCode))
	if err != nil {
		if errors.Is(err, mailimport.ErrNoCredentials) {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "请先填写邮箱地址与授权码"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mailbox": mailbox})
}

// discoverMailSenders 读取用户邮箱扫描窗口内出现过的发件人（带封数与
// 最近主题样本），供设置页勾选「账单邮件来源」。只读操作：不取正文、
// 不标记邮件。失败语义与 test 一致——HTTP 仍 200，由 ok/message 驱动
// 行内反馈（授权码无效、IMAP 未开启等都是「正常结果」）。
func (s *Server) discoverMailSenders(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	var in struct {
		Mailbox  string `json:"mailbox"`
		AuthCode string `json:"authCode"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	senders, err := s.mail.Discover(ctx, wid, strings.TrimSpace(in.Mailbox), strings.TrimSpace(in.AuthCode))
	if err != nil {
		if errors.Is(err, mailimport.ErrNoCredentials) {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "请先填写邮箱地址与授权码"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "senders": senders})
}

func (s *Server) startMailImport(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	if err := s.st.SetMailEnabled(r.Context(), wid, true); err != nil {
		mapStoreError(w, err)
		return
	}
	if err := s.mail.Start(r.Context(), wid); err != nil {
		// 启动失败回滚 enabled，保持「已停止」的一致状态（与助手同理）。
		_ = s.st.SetMailEnabled(r.Context(), wid, false)
		mapMailStartError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"running": true})
}

func (s *Server) stopMailImport(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	if err := s.st.SetMailEnabled(r.Context(), wid, false); err != nil {
		mapStoreError(w, err)
		return
	}
	s.mail.Stop(wid)
	writeJSON(w, http.StatusOK, map[string]any{"running": false})
}

func mapMailStartError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, mailimport.ErrNotConfigured):
		writeError(w, http.StatusNotFound, "not_found", "尚未配置邮箱账单导入，请先保存配置")
	case errors.Is(err, mailimport.ErrNoCredentials):
		writeError(w, http.StatusBadRequest, "no_credentials",
			"没有可用的邮箱凭据（邮箱地址/授权码未填写或已被撤销），请先保存配置")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "启动邮箱导入失败："+err.Error())
	}
}
