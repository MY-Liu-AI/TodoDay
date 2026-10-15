package parse

import (
	"testing"
	"time"
)

// 所有测试固定在一个日期与时区上，避免结果随运行时间漂移。
var (
	testNow = time.Date(2026, 10, 15, 10, 0, 0, 0, shanghai())
	testLoc = shanghai()
)

func shanghai() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.UTC
	}
	return loc
}

func TestParseRuleTable(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		title    string
		due      string
		priority int
		group    string
	}{
		{
			name:  "中文相对日期与分组与优先级",
			input: "明天 交周报 #工作 !!!",
			title: "交周报", due: "2026-10-16", priority: 1, group: "工作",
		},
		{
			name:  "英文相对日期",
			input: "tomorrow Send report #Work !!",
			title: "Send report", due: "2026-10-16", priority: 2, group: "Work",
		},
		{
			name:  "今天",
			input: "今天 打电话",
			title: "打电话", due: "2026-10-15", priority: 3, group: GroupLife,
		},
		{
			name:  "后天不等于明天",
			input: "后天 交材料",
			title: "交材料", due: "2026-10-17", priority: 3, group: GroupLife,
		},
		{
			name:  "day after tomorrow",
			input: "day after tomorrow Ship it",
			title: "Ship it", due: "2026-10-17", priority: 3, group: GroupLife,
		},
		{
			name:  "下周",
			input: "下周 体检",
			title: "体检", due: "2026-10-22", priority: 3, group: GroupLife,
		},
		{
			name:  "绝对日期 M月D日 未来",
			input: "12月1日 交年终总结",
			title: "交年终总结", due: "2026-12-01", priority: 3, group: GroupLife,
		},
		{
			name:  "绝对日期 M/D 过去则顺延一年",
			input: "1/5 报名",
			title: "报名", due: "2027-01-05", priority: 3, group: GroupLife,
		},
		{
			name:  "无日期",
			input: "买牛奶",
			title: "买牛奶", due: "", priority: 3, group: GroupLife,
		},
		{
			name:  "高优先级默认归工作",
			input: "!!! 处理线上故障",
			title: "处理线上故障", due: "", priority: 1, group: GroupWork,
		},
		{
			name:  "中优先级默认归生活",
			input: "!! 取快递",
			title: "取快递", due: "", priority: 2, group: GroupLife,
		},
		{
			name:  "全要素组合",
			input: "后天 跟老王开会 #客户 !",
			title: "跟老王开会", due: "2026-10-17", priority: 3, group: "客户",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseRule(tc.input, testNow, testLoc)
			if got.Title != tc.title {
				t.Errorf("标题：得到 %q，期望 %q", got.Title, tc.title)
			}
			if got.Due != tc.due {
				t.Errorf("日期：得到 %q，期望 %q", got.Due, tc.due)
			}
			if got.Priority != tc.priority {
				t.Errorf("优先级：得到 %d，期望 %d", got.Priority, tc.priority)
			}
			if got.Group != tc.group {
				t.Errorf("分组：得到 %q，期望 %q", got.Group, tc.group)
			}
		})
	}
}

// 空白输入不该产出标题：上层据此提示用户重新表述。
func TestParseRuleEmptyInput(t *testing.T) {
	for _, in := range []string{"", "   ", "\t\n"} {
		if got := ParseRule(in, testNow, testLoc); got.Title != "" {
			t.Errorf("输入 %q 应当得到空标题，得到 %q", in, got.Title)
		}
	}
}

// 感叹号必须独立成词才算优先级，不能误伤正常文本里的感叹号。
func TestBangMustStandAlone(t *testing.T) {
	// 中文字符不是空白，因此 "好!" 里的感叹号不构成优先级标记。
	got := ParseRule("好!", testNow, testLoc)
	if got.Priority != 3 {
		t.Errorf("紧贴文字的感叹号不应被当作优先级，得到 %d", got.Priority)
	}
	// 独立的三个感叹号才算。
	if p := ParseRule("买牛奶 !!!", testNow, testLoc); p.Priority != 1 {
		t.Errorf("独立感叹号应当识别为高优先级，得到 %d", p.Priority)
	}
}

// 时区必须影响「今天」的判定：同一时刻在 UTC 下可能还是前一天。
func TestParseRuleRespectsTimezone(t *testing.T) {
	utc := time.UTC
	// 2026-10-15 23:30 UTC = 2026-10-16 07:30 上海
	instant := time.Date(2026, 10, 15, 23, 30, 0, 0, time.UTC)
	gotSh := ParseRule("今天 开会", instant, testLoc)
	if gotSh.Due != "2026-10-16" {
		t.Errorf("上海时区下「今天」应为 2026-10-16，得到 %s", gotSh.Due)
	}
	gotUTC := ParseRule("今天 开会", instant, utc)
	if gotUTC.Due != "2026-10-15" {
		t.Errorf("UTC 下「今天」应为 2026-10-15，得到 %s", gotUTC.Due)
	}
}

// 非法日期不该被接受，也不该让标题被吞掉。
func TestParseRuleRejectsImpossibleDate(t *testing.T) {
	got := ParseRule("2月30日 开会", testNow, testLoc)
	// 2月30日 不存在：日期保持为空，文本留在标题里。
	if got.Due != "" {
		t.Errorf("不存在的日期应当被拒绝，得到 %q", got.Due)
	}
	if got.Title == "" {
		t.Error("标题不应为空")
	}
}

// 闰年边界：2024 是闰年，2月29日合法。
func TestParseRuleLeapDay(t *testing.T) {
	now := time.Date(2024, 1, 10, 10, 0, 0, 0, testLoc)
	got := ParseRule("2月29日 交材料", now, testLoc)
	if got.Due != "2024-02-29" {
		t.Errorf("2024-02-29 应当合法，得到 %q", got.Due)
	}
}

// 末尾标签与优先级清理后，标题不该残留多余空格。
func TestParseRuleTrimsExtraSpace(t *testing.T) {
	got := ParseRule("明天   交   周报   #工作  !!!", testNow, testLoc)
	if got.Title != "交 周报" {
		t.Errorf("标题应当折叠多余空白，得到 %q", got.Title)
	}
}
