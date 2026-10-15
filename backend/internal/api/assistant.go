package api

import (
	"errors"
	"net/http"
	"strings"

	"tododay/internal/store"
)

// 助手配置的三个接口，职责刻意分开：
//
//	GET   掩码视图（成员可见）—— 永不返回密钥本身，前端据此渲染「已配置」
//	PUT   保存（仅 owner）    —— 密钥字段留空表示保留原值
//	GET   credentials（仅 owner）—— 唯一返回明文的接口，供助手进程取用
//
// 把「能看到有没有配」与「能取到密钥」分成两个权限级别，是因为前者是
// 展示需要，后者等于把凭据交出去。

// 字段长度上限。不是密钥，只是防脏数据。
const (
	maxBotIDLen   = 128
	maxBaseURLLen = 200
	maxModelLen   = 100
)

// assistantView 是掩码视图。注意这里**没有** botSecret / kimiApiKey 字段 ——
// 只有「是否已设置」的布尔值。
func assistantView(cfg *store.AssistantConfig, boxAvailable bool) map[string]any {
	if cfg == nil {
		return map[string]any{
			"configured":          false,
			"enabled":             false,
			"botId":               "",
			"botSecretSet":        false,
			"kimiApiKeySet":       false,
			"kimiBase":            "",
			"kimiModel":           "",
			"version":             0,
			"encryptionAvailable": boxAvailable,
		}
	}
	return map[string]any{
		"configured":          true,
		"enabled":             cfg.Enabled,
		"botId":               cfg.BotID,
		"botSecretSet":        cfg.BotSecretEnc != "",
		"kimiApiKeySet":       cfg.KimiAPIKeyEnc != "",
		"kimiBase":            cfg.KimiBase,
		"kimiModel":           cfg.KimiModel,
		"version":             cfg.Version,
		"encryptionAvailable": boxAvailable,
	}
}

// 未配置时返回空视图而不是 404：对前端来说「还没配」是正常状态，
// 不该被当成错误处理。
func (s *Server) getAssistantConfig(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	cfg, err := s.st.GetAssistantConfig(r.Context(), wid)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, assistantView(nil, s.box != nil))
		return
	}
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, assistantView(cfg, s.box != nil))
}

func (s *Server) putAssistantConfig(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	// 密钥用 *string：需要区分「没传/传空」与「传了值」。
	// decodeJSON 开了 DisallowUnknownFields，前端多发一个字段就会 400。
	var in struct {
		Enabled    bool    `json:"enabled"`
		BotID      string  `json:"botId"`
		BotSecret  *string `json:"botSecret"`
		KimiAPIKey *string `json:"kimiApiKey"`
		KimiBase   string  `json:"kimiBase"`
		KimiModel  string  `json:"kimiModel"`
		Version    int64   `json:"version"`
		// 显式撤销。留空只表示「不修改」，所以要删掉已存凭据必须明说。
		ClearBotSecret  bool `json:"clearBotSecret"`
		ClearKimiAPIKey bool `json:"clearKimiApiKey"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}

	botID := strings.TrimSpace(in.BotID)
	base := strings.TrimSpace(in.KimiBase)
	model := strings.TrimSpace(in.KimiModel)
	if len([]rune(botID)) > maxBotIDLen {
		writeError(w, http.StatusBadRequest, "validation_failed", "BotID 过长")
		return
	}
	if len([]rune(base)) > maxBaseURLLen {
		writeError(w, http.StatusBadRequest, "validation_failed", "Kimi 接口地址过长")
		return
	}
	if len([]rune(model)) > maxModelLen {
		writeError(w, http.StatusBadRequest, "validation_failed", "模型名过长")
		return
	}

	// 只处理「用户确实输入了值」的情况；空/未传一律交给 store 保留原值。
	secrets := []*string{in.BotSecret, in.KimiAPIKey}
	hasNewSecret := false
	for _, sp := range secrets {
		if sp != nil && strings.TrimSpace(*sp) != "" {
			hasNewSecret = true
		}
	}
	if hasNewSecret && s.box == nil {
		// 绝不退化成明文存储：没有主密钥就直接拒绝，并说清怎么配。
		writeError(w, http.StatusBadRequest, "encryption_unavailable",
			"服务端未配置主密钥，无法保存密钥。请设置环境变量 TODO_CONFIG_KEY（32 字节 base64）后重启服务。")
		return
	}

	// 撤销不需要主密钥：那是删除而不是存储，缺密钥时也应当允许 ——
	// 否则主密钥一丢，用户连清理自己凭据的办法都没有。
	input := store.AssistantConfigInput{
		Enabled:         in.Enabled,
		BotID:           botID,
		KimiBase:        base,
		KimiModel:       model,
		ClearBotSecret:  in.ClearBotSecret,
		ClearKimiAPIKey: in.ClearKimiAPIKey,
	}
	if s.box != nil {
		if in.BotSecret != nil && strings.TrimSpace(*in.BotSecret) != "" {
			enc, err := s.box.Seal(strings.TrimSpace(*in.BotSecret))
			if err != nil {
				mapStoreError(w, err)
				return
			}
			input.BotSecretEnc = enc
		}
		if in.KimiAPIKey != nil && strings.TrimSpace(*in.KimiAPIKey) != "" {
			enc, err := s.box.Seal(strings.TrimSpace(*in.KimiAPIKey))
			if err != nil {
				mapStoreError(w, err)
				return
			}
			input.KimiAPIKeyEnc = enc
		}
	}

	cfg, err := s.st.SaveAssistantConfig(r.Context(), wid, u.ID, input, in.Version)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, assistantView(cfg, s.box != nil))
}

// assistantCredentials 是唯一返回明文的接口。权限是 owner：
// 能取到密钥就等于能冒充这个机器人。
func (s *Server) assistantCredentials(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	cfg, err := s.st.GetAssistantConfig(r.Context(), wid)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "尚未配置助手")
		return
	}
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if s.box == nil {
		writeError(w, http.StatusBadRequest, "encryption_unavailable",
			"服务端未配置主密钥，无法读取密钥。请设置环境变量 TODO_CONFIG_KEY 后重启服务。")
		return
	}

	botSecret, err := s.box.Unseal(cfg.BotSecretEnc)
	if err != nil {
		// 主密钥换过或数据损坏，两者都无法自行恢复 —— 让用户重新填。
		writeError(w, http.StatusBadRequest, "decrypt_failed",
			"密钥无法解密（主密钥可能已更换），请在设置页重新填写。")
		return
	}
	kimiKey, err := s.box.Unseal(cfg.KimiAPIKeyEnc)
	if err != nil {
		writeError(w, http.StatusBadRequest, "decrypt_failed",
			"Kimi Key 无法解密（主密钥可能已更换），请在设置页重新填写。")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":    cfg.Enabled,
		"botId":      cfg.BotID,
		"botSecret":  botSecret,
		"kimiApiKey": kimiKey,
		"kimiBase":   cfg.KimiBase,
		"kimiModel":  cfg.KimiModel,
	})
}
