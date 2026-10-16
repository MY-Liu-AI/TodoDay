package parse

import "time"

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
