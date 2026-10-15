package parse

import (
	"context"
	"log"
	"strings"
	"time"
)

// Source 说明结果来自哪条路径。写进回执文案，对用户透明，也便于排查
// 「为什么这句没听懂」。
type Source string

const (
	SourceKimi Source = "kimi"
	SourceRule Source = "rule"
)

// Parser 组合 Kimi 与规则解析。
//
// 降级边界（任一情况都退回规则解析）：
//   - 未配置 API key
//   - 调用出错、超时
//   - 响应里没有可用的任务内容
//
// 换句话说：AI 是加速器，不是单点。没有它服务照样能用。
type Parser struct {
	kimi *Kimi // 为 nil 表示未配置
	loc  *time.Location
}

// NewParser 构造解析器。kimi 为 nil 时全部走规则解析。
func NewParser(kimi *Kimi, loc *time.Location) *Parser {
	if loc == nil {
		loc = time.UTC
	}
	return &Parser{kimi: kimi, loc: loc}
}

// Location 返回工作区时区，供回执文案复用。
func (p *Parser) Location() *time.Location { return p.loc }

// Parse 解析一句话，返回结果与来源。
func (p *Parser) Parse(ctx context.Context, text string) (Plan, Source) {
	now := time.Now()
	if p.kimi != nil {
		plan, err := p.kimi.Parse(ctx, text, now, p.loc)
		if err == nil {
			return plan, SourceKimi
		}
		// 失败要记日志：静默降级会让人以为 AI 在正常工作。
		log.Printf("parse: Kimi 解析失败，改用规则解析：%v", err)
	}
	return ParseRule(text, now, p.loc), SourceRule
}

// IsEmpty 判断文本是否没有任何可记录的内容。
func IsEmpty(text string) bool { return strings.TrimSpace(text) == "" }
