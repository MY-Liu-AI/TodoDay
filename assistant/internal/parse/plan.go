// Package parse 把一句中文或英文的计划解析成结构化任务字段。
//
// 只走 AI（Kimi）：用 function calling 拿结构化结果。未配置 API key 或调用
// 失败时直接返回 error，由编排层如实告知用户——不再有关键词规则兜底，
// 也不静默成功。产出的字段必须落在服务端可接受范围内，否则建任务会被 400 拒绝。
package parse

import (
	"fmt"
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

// dateValue 把一个时刻按工作区时区格式化成 YYYY-MM-DD，供提示词注入当天日期。
func dateValue(t time.Time, loc *time.Location) string {
	y, m, d := t.In(loc).Date()
	return fmt.Sprintf("%04d-%02d-%02d", y, int(m), d)
}
