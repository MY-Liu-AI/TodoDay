// Package parse 把一句中文或英文的计划解析成结构化任务字段。
//
// 两条路径：优先调 Claude（理解口语化表达），失败或未配置时退回规则解析。
// 两条路径都必须产出服务端能接受的字段，否则建任务会被 400 拒绝。
package parse

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Plan 是解析结果。字段命名对应后端建任务接口的入参。
type Plan struct {
	Title    string // 任务内容，1–240 字
	Due      string // YYYY-MM-DD；"" 表示不排期
	Priority int    // 1 高 / 2 中 / 3 普通
	Group    string // 分组名，默认 工作 或 生活
}

// 默认分组与前端保持一致：高优先级归 工作，其余归 生活。
const (
	GroupWork = "工作"
	GroupLife = "生活"
)

var (
	// 匹配独立的 ! 串，前后必须是空白或串首尾，避免把 "好!" 里的感叹号当优先级。
	bangRE = regexp.MustCompile(`(^|\s)(!{1,3})(\s|$)`)
	// 标签：#后面到空白为止。
	tagRE = regexp.MustCompile(`#(\S+)`)
	// 绝对日期：3月5日 / 3/5 / 3-5。
	absDateRE = regexp.MustCompile(`(\d{1,2})[/月-](\d{1,2})[日号]?`)
	spaceRE   = regexp.MustCompile(`\s+`)
)

// 相对日期规则，顺序不能改：长匹配置前。
// "day after tomorrow" 含 "tomorrow"，"后天" 必须先于 "明天" 判断才安全。
type relRule struct {
	re    *regexp.Regexp
	shift int // 相对今天的天数
}

var relRules = []relRule{
	{regexp.MustCompile(`(?i)后天|day\s+after\s+tomorrow`), 2},
	{regexp.MustCompile(`(?i)今天|today`), 0},
	{regexp.MustCompile(`(?i)明天|tomorrow`), 1},
	{regexp.MustCompile(`(?i)下周`), 7},
}

// ParseRule 是规则解析器：移植前端 src/lib/todo.js 的 parseInput 语义，
// 让「企业微信里说的」和「网页上打的」得到同样的结果。
//
// 支持的语法：
//
//	明天 交周报 #工作 !!!      → 标题「交周报」，明天，高优先级，分组 工作
//	tomorrow Send report #Work → 同上（英文）
//	3月5日 体检                → 绝对日期
func ParseRule(text string, now time.Time, loc *time.Location) Plan {
	t := strings.TrimSpace(text)
	p := 3
	due := ""

	// 优先级：!!! → 1、!! → 2、! → 3
	if m := bangRE.FindStringSubmatch(t); m != nil {
		p = 4 - len(m[2])
		t = strings.Replace(t, m[0], " ", 1)
	}

	// 日期：命中一条即停。
	for _, r := range relRules {
		if m := r.re.FindStringIndex(t); m != nil {
			due = dateValue(now.In(loc).AddDate(0, 0, r.shift), loc)
			t = t[:m[0]] + " " + t[m[1]:]
			break
		}
	}
	if due == "" {
		if m := absDateRE.FindStringSubmatch(t); m != nil {
			if d, ok := absDate(m[1], m[2], now, loc); ok {
				due = d
				t = strings.Replace(t, m[0], " ", 1)
			}
		}
	}

	// 分组：#标签
	group := ""
	if m := tagRE.FindStringSubmatch(t); m != nil {
		group = m[1]
		t = strings.Replace(t, m[0], " ", 1)
	}

	t = strings.TrimSpace(spaceRE.ReplaceAllString(t, " "))
	if group == "" {
		if p == 1 {
			group = GroupWork
		} else {
			group = GroupLife
		}
	}

	return Plan{Title: t, Due: due, Priority: p, Group: group}
}

// absDate 解析 M月D日 / M/D。解析出的日期若已过去，顺延到下一年 ——
// 与前端行为一致：说「1月2日」时如果今年已经过了，指的显然是明年。
func absDate(monthStr, dayStr string, now time.Time, loc *time.Location) (string, bool) {
	var month, day int
	if _, err := fmt.Sscanf(monthStr, "%d", &month); err != nil {
		return "", false
	}
	if _, err := fmt.Sscanf(dayStr, "%d", &day); err != nil {
		return "", false
	}
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return "", false
	}
	today := now.In(loc)
	candidate := time.Date(today.Year(), time.Month(month), day, 0, 0, 0, 0, loc)
	// 校验没被规范化（比如 2月30日 会滚动到 3月）。
	if candidate.Month() != time.Month(month) || candidate.Day() != day {
		return "", false
	}
	if candidate.Before(time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)) {
		candidate = time.Date(today.Year()+1, time.Month(month), day, 0, 0, 0, 0, loc)
	}
	return dateValue(candidate, loc), true
}

func dateValue(t time.Time, loc *time.Location) string {
	y, m, d := t.In(loc).Date()
	return fmt.Sprintf("%04d-%02d-%02d", y, int(m), d)
}
