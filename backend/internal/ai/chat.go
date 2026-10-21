// Package ai 是工作区 AI 功能的能力层：技能注册表 + 调用器。
//
// 架构约定（改动前读）：
//  1. 注册表是唯一事实源——适配层（HTTP / 企业微信 / 未来的批处理）
//     不许出现按技能名的 if/else 分支；按技能声明的 context/output 键
//     在已注册的 provider/validator 表里查找。
//  2. Skill 之间禁止互相调用、保持无状态；编排（多步串联）是适配层的事。
//  3. AI 产出若落库必须带 schema 版本号；当前各技能的产出均为一次性返回。
package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ChatMessage / ChatRequest / ChatResponse 是 OpenAI 兼容的聊天协议类型，
// 从 parse 包上移到能力层：助手解析（function calling）与技能调用
// （结构化 JSON 输出）共用同一条传输实现，避免两个 Kimi 客户端各自漂移。
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model      string           `json:"model"`
	Messages   []ChatMessage    `json:"messages"`
	Tools      []map[string]any `json:"tools,omitempty"`
	ToolChoice string           `json:"tool_choice,omitempty"`
	// Stream 为 true 时走 SSE：k2/k3 这类推理模型的非流式接口会一直憋到
	// 思考结束才吐响应头（实测 kimi-k2.6 轻松超过 2 分钟），而流式首包
	// 1 秒左右。技能调用（invoke.go）固定开流式；助手解析保持非流式
	// （function calling 请求小、响应短）。服务端若忽略 stream 字段
	// 直接回整包 JSON，doChat 会按 Content-Type 自动退回普通解码。
	Stream bool `json:"stream,omitempty"`
	// Temperature 故意不发：Moonshot 的 kimi-k2 系列只允许 temperature=1，
	// 显式发 0.1 这类「抽取任务低温」会被 400 拒绝（invalid temperature）；
	// 各模型的默认值由服务端自己保证合法。
	Temperature *float64 `json:"temperature,omitempty"`
}

type ChatResponse struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name string `json:"name"`
					// OpenAI 兼容格式里，工具参数是 JSON 字符串而不是对象。
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// DoChat 发一次 /chat/completions 并解码响应。httpClient 由调用方给：
// parse.Kimi 用 SSRF 收紧过的客户端（不跟随重定向），技能调用同样复用，
// 防止 owner 可配置的 base 302 到内网把 Bearer 密钥带过去。
func DoChat(ctx context.Context, httpClient *http.Client, base, apiKey string, req *ChatRequest) (*ChatResponse, error) {
	return doChat(ctx, httpClient, base, apiKey, req, false)
}

// DoChatLenient 与 DoChat 相同，但容忍空 choices：「测试连接」类调用
// 只验证 key/地址/模型可用，模型不产出内容也算通过。
func DoChatLenient(ctx context.Context, httpClient *http.Client, base, apiKey string, req *ChatRequest) (*ChatResponse, error) {
	return doChat(ctx, httpClient, base, apiKey, req, true)
}

// doChat 是 DoChat 的实现。allowEmpty 为 true 时容忍空 choices——
// 「测试连接」只验证 key/地址/模型可用，模型不产出内容也算通。
func doChat(ctx context.Context, httpClient *http.Client, base, apiKey string, req *ChatRequest, allowEmpty bool) (*ChatResponse, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("调用模型失败：%w", err)
	}
	defer resp.Body.Close()

	// 流式响应：累计 delta.content 直到 finish_reason 或 [DONE]，
	// 聚合成与非流式相同的 ChatResponse 形状，调用方无感。
	if req.Stream && strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		return readSSE(resp, allowEmpty)
	}

	var out ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("模型响应不是合法 JSON：%w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if out.Error != nil {
			return nil, fmt.Errorf("模型返回 %d：%s", resp.StatusCode, out.Error.Message)
		}
		return nil, fmt.Errorf("模型返回 %d", resp.StatusCode)
	}
	if len(out.Choices) == 0 && !allowEmpty {
		return nil, fmt.Errorf("模型没有返回任何结果")
	}
	return &out, nil
}

// readSSE 解析 OpenAI 兼容的流式响应：逐行读 "data: {...}"，
// 累计 choices[0].delta.content，遇 finish_reason 或 [DONE] 结束。
// 聚合成与非流式一致的 ChatResponse，调用方校验逻辑不用分流式/非流式。
// 非 200 的流式错误（部分服务商直接流式吐错误 JSON）按 out.Error 处理。
func readSSE(resp *http.Response, allowEmpty bool) (*ChatResponse, error) {
	var out ChatResponse
	scanner := bufio.NewScanner(resp.Body)
	// chunk 行可能较长（思考型模型单 chunk 也有几百字节），放宽行上限。
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(data) == 0 {
			continue
		}
		if bytes.Equal(data, []byte("[DONE]")) {
			break
		}
		var ev struct {
			Choices []struct {
				FinishReason string `json:"finish_reason"`
				Delta        struct {
					Content string `json:"content"`
				} `json:"delta"`
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			Error *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			continue // 心跳/注释行，忽略
		}
		if ev.Error != nil {
			return nil, fmt.Errorf("模型返回流式错误：%s", ev.Error.Message)
		}
		for _, ch := range ev.Choices {
			if len(out.Choices) == 0 {
				out.Choices = append(out.Choices, struct {
					FinishReason string `json:"finish_reason"`
					Message      struct {
						Content   string `json:"content"`
						ToolCalls []struct {
							Function struct {
								Name string `json:"name"`
								// OpenAI 兼容格式里，工具参数是 JSON 字符串而不是对象。
								Arguments string `json:"arguments"`
							} `json:"function"`
						} `json:"tool_calls"`
					} `json:"message"`
				}{})
			}
			out.Choices[0].Message.Content += ch.Delta.Content + ch.Message.Content
			if ch.FinishReason != "" {
				out.Choices[0].FinishReason = ch.FinishReason
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取模型流式响应失败：%w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("模型返回 %d", resp.StatusCode)
	}
	if len(out.Choices) == 0 && !allowEmpty {
		return nil, fmt.Errorf("模型没有返回任何结果")
	}
	return &out, nil
}
