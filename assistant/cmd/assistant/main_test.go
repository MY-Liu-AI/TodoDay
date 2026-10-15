package main

import (
	"testing"
)

// 长连接模式下不再有回调握手，这个入口只保留不依赖网络的纯函数测试。
func TestDueOrNone(t *testing.T) {
	if dueOrNone("") != "未排期" {
		t.Error("空日期应当显示未排期")
	}
	if dueOrNone("2026-10-16") != "2026-10-16" {
		t.Error("有日期时应原样返回")
	}
}
