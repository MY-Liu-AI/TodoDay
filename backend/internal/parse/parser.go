package parse

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Parser 用 AI（Kimi）把一句话解析成结构化任务字段。
//
// 不再有关键词兜底：未配置 API key 或调用失败时直接返回 error，由编排层
// 如实告知用户——不静默成功、不退回规则解析。
type Parser struct {
	kimi *Kimi // 为 nil 表示未配置
	loc  *time.Location
}

// NewParser 构造解析器。kimi 为 nil 时 Parse 会返回「未配置 AI 解析」错误。
func NewParser(kimi *Kimi, loc *time.Location) *Parser {
	if loc == nil {
		loc = time.UTC
	}
	return &Parser{kimi: kimi, loc: loc}
}

// Location 返回工作区时区，供回执文案复用。
func (p *Parser) Location() *time.Location { return p.loc }

// Parse 用 AI 解析一句话。未配置或调用失败时返回 error，不再降级。
func (p *Parser) Parse(ctx context.Context, text string) (Plan, error) {
	if p.kimi == nil {
		return Plan{}, fmt.Errorf("未配置 AI 解析")
	}
	return p.kimi.Parse(ctx, text, time.Now(), p.loc)
}

// IsEmpty 判断文本是否没有任何可记录的内容。
func IsEmpty(text string) bool { return strings.TrimSpace(text) == "" }
