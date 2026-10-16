package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"tododay/internal/aibot"
	"tododay/internal/assistant"
	"tododay/internal/parse"
	"tododay/internal/store"
)

// 助手配置与运行控制接口，职责刻意分开：
//
//	GET   掩码视图（成员可见）—— 永不返回密钥本身，前端据此渲染「已配置/运行中」
//	PUT   保存（仅 owner）    —— 密钥字段留空表示保留原值；不改变启用状态
//	GET   credentials（仅 owner）—— 唯一返回明文的接口，供独立助手进程取用
//	POST  test/start/stop（仅 owner）—— 内嵌运行：测试凭据、启动、停止
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
// 只有「是否已设置」的布尔值。running 来自内嵌 manager，与 enabled（持久化
// 意图）分开：前者是「此刻有没有一条连接在跑」。
func assistantView(cfg *store.AssistantConfig, boxAvailable bool, running bool) map[string]any {
	if cfg == nil {
		return map[string]any{
			"configured":          false,
			"enabled":             false,
			"running":             false,
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
		"running":             running,
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
		writeJSON(w, http.StatusOK, assistantView(nil, s.box != nil, false))
		return
	}
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, assistantView(cfg, s.box != nil, s.assistant.IsRunning(wid)))
}

func (s *Server) putAssistantConfig(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	// 密钥用 *string：需要区分「没传/传空」与「传了值」。
	// decodeJSON 开了 DisallowUnknownFields，前端多发一个字段就会 400。
	//
	// 注意这里**没有** enabled 字段：启用/停用改由 start/stop 端点控制，
	// 保存配置不该顺带把开关改掉。
	var in struct {
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

	// 保留当前的 enabled：启动/停止已由 start/stop 端点控制，配置保存不得改写它。
	enabled := false
	if cur, err := s.st.GetAssistantConfig(r.Context(), wid); err == nil {
		enabled = cur.Enabled
	} else if !errors.Is(err, store.ErrNotFound) {
		mapStoreError(w, err)
		return
	}

	// 撤销不需要主密钥：那是删除而不是存储，缺密钥时也应当允许 ——
	// 否则主密钥一丢，用户连清理自己凭据的办法都没有。
	input := store.AssistantConfigInput{
		Enabled:         enabled,
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
	writeJSON(w, http.StatusOK, assistantView(cfg, s.box != nil, s.assistant.IsRunning(wid)))
}

// assistantCredentials 是唯一返回明文的接口。权限是 owner：
// 能取到密钥就等于能冒充这个机器人。供独立 assistant 进程兜底使用。
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

// testAssistant 验证机器人凭据能否真连上企业微信（做一次真实订阅）。
//
// 测的是「表单里此刻填的值」，不是已保存值：第一次配置时用户还没保存，却需要
// 先验证凭据。botId/secret 留空才回退到已保存配置。凭据可解且不为空时，无论
// 测试成不成功都返回 200（测试失败是正常结果），只有「没东西可测/无法解密」才 4xx。
func (s *Server) testAssistant(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	var in struct {
		BotID     string  `json:"botId"`
		BotSecret *string `json:"botSecret"`
	}
	if err := decodeOptionalJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}

	// 已保存配置只作回退；不存在（还没配过）不算错。
	var cfg *store.AssistantConfig
	if c, err := s.st.GetAssistantConfig(r.Context(), wid); err == nil {
		cfg = c
	} else if !errors.Is(err, store.ErrNotFound) {
		mapStoreError(w, err)
		return
	}

	botID := strings.TrimSpace(in.BotID)
	if botID == "" && cfg != nil {
		botID = cfg.BotID
	}

	// Secret：这次填了就用这次填的（明文随请求临时传来，不落库）；留空才回退
	// 到已保存密文并解密。这与「留空 = 保留原值」的保存语义保持一致。
	secret := ""
	if in.BotSecret != nil && strings.TrimSpace(*in.BotSecret) != "" {
		secret = strings.TrimSpace(*in.BotSecret)
	} else if cfg != nil && cfg.BotSecretEnc != "" {
		if s.box == nil {
			writeError(w, http.StatusBadRequest, "encryption_unavailable",
				"服务端未配置主密钥，无法读取机器人 Secret。请设置环境变量 TODO_CONFIG_KEY 后重启服务。")
			return
		}
		unsealed, err := s.box.Unseal(cfg.BotSecretEnc)
		if err != nil || unsealed == "" {
			writeError(w, http.StatusBadRequest, "decrypt_failed",
				"机器人 Secret 无法解密（主密钥可能已更换），请在设置页重新填写。")
			return
		}
		secret = unsealed
	}

	if botID == "" || secret == "" {
		writeError(w, http.StatusBadRequest, "not_configured", "还没有可测试的机器人凭据（BotID/Secret）")
		return
	}

	// 同一个机器人只允许一条有效长连接：正在跑的内嵌连接会和这里的测试订阅
	// 互踢，导致测试等不到订阅回执。先停掉再测，测完恢复原状。enabled 持久化
	// 意图不变，这里只是暂时断开（若用户在跑，测试后恢复它）。
	wasRunning := s.assistant.IsRunning(wid)
	if wasRunning {
		s.assistant.Stop(wid)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	ok, errcode, errmsg, verr := aibot.Verify(ctx, botID, secret, "")

	// 恢复原来的运行状态（无论测试成败）。用独立上下文：请求上下文可能已被
	// 取消，而 manager.Start 只拿它读 store，不会成为 run 的父上下文。
	if wasRunning {
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 10*time.Second)
		if rerr := s.assistant.Start(restoreCtx, wid, u.ID); rerr != nil {
			log.Printf("assistant: 测试后恢复工作区 %d 助手失败：%v", wid, rerr)
		}
		restoreCancel()
	}

	kimiConfigured := cfg != nil && cfg.KimiAPIKeyEnc != ""
	if verr != nil {
		log.Printf("assistant: 工作区 %d 测试连接失败：%v", wid, verr)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":             false,
			"errcode":        errcode,
			"errmsg":         errmsg,
			"message":        verr.Error(),
			"kimiConfigured": kimiConfigured,
		})
		return
	}
	if ok {
		log.Printf("assistant: 工作区 %d 测试连接通过", wid)
	} else {
		log.Printf("assistant: 工作区 %d 测试连接被拒 errcode=%d errmsg=%s", wid, errcode, errmsg)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             ok,
		"errcode":        errcode,
		"errmsg":         errmsg,
		"kimiConfigured": kimiConfigured,
	})
}

// testAssistantLlm 验证 LLM（Kimi）凭证能否真调通：用一次最小调用探测
// key / 接口地址 / 模型是否都生效。与机器人「测试连接」同理：测的是表单里
// 此刻填的值，key/地址/模型留空才回退到已保存配置；测试失败是正常结果
// （HTTP 仍是 200），只有「没东西可测/无法解密」才返回 4xx。
func (s *Server) testAssistantLlm(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	// 注意：这里**故意不收 kimiBase**。接口地址是 owner 可任意配置的 URL，
	// 若让它随测试请求传入，就等于给测试端点开了一个新的 SSRF 起点（指向内网
	// 地址时服务器会替它发请求）。地址只能通过保存路径（owner-only + 长度校验）
	// 落库，测试只用已保存地址。key 与模型不是 URL、不构成 SSRF，才允许「先测后存」。
	var in struct {
		KimiAPIKey *string `json:"kimiApiKey"`
		KimiModel  string  `json:"kimiModel"`
	}
	if err := decodeOptionalJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}

	// 已保存配置只作回退；不存在（还没配过）不算错。
	var cfg *store.AssistantConfig
	if c, err := s.st.GetAssistantConfig(r.Context(), wid); err == nil {
		cfg = c
	} else if !errors.Is(err, store.ErrNotFound) {
		mapStoreError(w, err)
		return
	}

	// Key：这次填了就用这次填的（明文随请求临时传来，不落库）；留空才回退
	// 到已保存密文并解密。
	key := ""
	if in.KimiAPIKey != nil && strings.TrimSpace(*in.KimiAPIKey) != "" {
		key = strings.TrimSpace(*in.KimiAPIKey)
	} else if cfg != nil && cfg.KimiAPIKeyEnc != "" {
		if s.box == nil {
			writeError(w, http.StatusBadRequest, "encryption_unavailable",
				"服务端未配置主密钥，无法读取 LLM API Key。请设置环境变量 TODO_CONFIG_KEY 后重启服务。")
			return
		}
		unsealed, err := s.box.Unseal(cfg.KimiAPIKeyEnc)
		if err != nil || unsealed == "" {
			writeError(w, http.StatusBadRequest, "decrypt_failed",
				"LLM API Key 无法解密（主密钥可能已更换），请在设置页重新填写。")
			return
		}
		key = unsealed
	}
	if key == "" {
		writeError(w, http.StatusBadRequest, "not_configured", "还没有可测试的 LLM API Key")
		return
	}

	// 地址只用已保存值（可能为空，由 parse.NewKimi 落到默认值）；模型这次填了
	// 就用这次填的，否则回退已保存值。两者与解析路径一致。
	base := ""
	if cfg != nil {
		base = cfg.KimiBase
	}
	model := strings.TrimSpace(in.KimiModel)
	if model == "" && cfg != nil {
		model = cfg.KimiModel
	}
	k := parse.NewKimi(key, base, model, 10*time.Second)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if verr := k.Verify(ctx); verr != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": verr.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": ""})
}

// startAssistant 持久化「启用」意图并启动内嵌连接。
func (s *Server) startAssistant(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	if err := s.st.SetAssistantEnabled(r.Context(), wid, true); err != nil {
		mapStoreError(w, err)
		return
	}
	if err := s.assistant.Start(r.Context(), wid, u.ID); err != nil {
		// 启动失败则回滚 enabled，保持「已停止」的一致状态，避免下次启动时
		// SyncAll 反复尝试一个永远起不来的工作区。
		_ = s.st.SetAssistantEnabled(r.Context(), wid, false)
		mapAssistantStartError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"running": true})
}

// stopAssistant 持久化「停用」意图并停止内嵌连接。
func (s *Server) stopAssistant(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	if err := s.st.SetAssistantEnabled(r.Context(), wid, false); err != nil {
		mapStoreError(w, err)
		return
	}
	s.assistant.Stop(wid)
	writeJSON(w, http.StatusOK, map[string]any{"running": false})
}

func mapAssistantStartError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, assistant.ErrNotConfigured):
		writeError(w, http.StatusNotFound, "not_found", "尚未配置助手，请先保存 BotID/Secret")
	case errors.Is(err, assistant.ErrEncryptionUnavailable):
		writeError(w, http.StatusBadRequest, "encryption_unavailable",
			"服务端未配置主密钥，无法解密机器人 Secret。请设置环境变量 TODO_CONFIG_KEY 后重启服务。")
	case errors.Is(err, aibot.ErrNoCredentials):
		writeError(w, http.StatusBadRequest, "not_configured",
			"没有可用的机器人凭据（BotID/Secret 未填写或已被撤销）")
	case errors.Is(err, aibot.ErrDisabled):
		writeError(w, http.StatusBadRequest, "disabled", "助手已停用")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "启动助手失败："+err.Error())
	}
}
