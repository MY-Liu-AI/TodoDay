package parse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// billMockServer 返回一个假的 /chat/completions：decide 按请求内容
// 决定 record_bill 的参数。
func billMockServer(t *testing.T, decide func(userText string) string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("解码请求失败：%v", err)
			return
		}
		userText := ""
		for _, m := range req.Messages {
			if m.Role == "user" {
				userText = m.Content
			}
		}
		args := decide(userText)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"record_bill","arguments":%q}}]}}]}`, args)
	}))
}

func newBillKimi(t *testing.T, srv *httptest.Server) *Kimi {
	t.Helper()
	return NewKimi("test-key", srv.URL, "", 5*time.Second)
}

func TestParseBillExpense(t *testing.T) {
	srv := billMockServer(t, func(string) string {
		return `{"is_bill":true,"type":"exp","amount_yuan":88.5,"category":"餐饮","date":"2026-10-26","merchant":"某某超市"}`
	})
	defer srv.Close()
	k := newBillKimi(t, srv)
	loc, _ := time.LoadLocation("Asia/Shanghai")
	cats := []string{"餐饮", "交通", "购物", "住房", "娱乐", "其他"}
	bill, err := k.ParseBill(context.Background(), "bank@example.com", "您有一笔新的消费", "正文", time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC), loc, cats)
	if err != nil {
		t.Fatal(err)
	}
	if !bill.IsBill || bill.Type != "exp" || bill.AmountFen != 8850 || bill.Category != "餐饮" || bill.Date != "2026-10-26" || bill.Merchant != "某某超市" {
		t.Fatalf("解析结果不符：%+v", bill)
	}
}

// system prompt 必须携带工作区分类清单：解析按用户调整后的分类收敛。
func TestParseBillPromptListsCategories(t *testing.T) {
	var system string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("解码请求失败：%v", err)
			return
		}
		for _, m := range req.Messages {
			if m.Role == "system" {
				system = m.Content
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"record_bill","arguments":"{\"is_bill\":false}"}}]}}]}`)
	}))
	defer srv.Close()
	k := newBillKimi(t, srv)
	cats := []string{"餐饮", "交通", "自建分类"}
	if _, err := k.ParseBill(context.Background(), "a@b", "s", "b", time.Now(), time.UTC, cats); err != nil {
		t.Fatal(err)
	}
	for _, c := range cats {
		if !strings.Contains(system, c) {
			t.Fatalf("system prompt 应包含分类 %q：\n%s", c, system)
		}
	}
}

// 分类收敛：模型给出的分类不在清单内一律归「其他」；清单没有「其他」
// 则留空（调用方按同一约定兜底）；清单为空用默认清单。
func TestParseBillCategoryClamp(t *testing.T) {
	cases := []struct {
		name     string
		modelCat string
		cats     []string
		want     string
	}{
		{"清单内保留", "交通", []string{"餐饮", "交通", "其他"}, "交通"},
		{"清单外归其他", "乱七八糟", []string{"餐饮", "交通", "其他"}, "其他"},
		{"清单无其他则留空", "乱七八糟", []string{"餐饮", "交通"}, ""},
		{"空清单用默认", "住房", nil, "住房"},
		{"空清单外归其他", "乱七八糟", nil, "其他"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := billMockServer(t, func(string) string {
				return fmt.Sprintf(`{"is_bill":true,"type":"exp","amount_yuan":1,"category":%q,"date":"2026-10-26","merchant":"m"}`, tc.modelCat)
			})
			defer srv.Close()
			k := newBillKimi(t, srv)
			bill, err := k.ParseBill(context.Background(), "a@b", "s", "b", time.Now(), time.UTC, tc.cats)
			if err != nil {
				t.Fatal(err)
			}
			if bill.Category != tc.want {
				t.Fatalf("分类应收敛为 %q，得到 %q", tc.want, bill.Category)
			}
		})
	}
}

func TestParseBillNotABill(t *testing.T) {
	srv := billMockServer(t, func(string) string {
		return `{"is_bill":false,"type":"","amount_yuan":0,"category":"","date":"","merchant":""}`
	})
	defer srv.Close()
	k := newBillKimi(t, srv)
	loc := time.UTC
	bill, err := k.ParseBill(context.Background(), "promo@example.com", "周末观影优惠", "正文", time.Now(), loc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bill.IsBill || bill.AmountFen != 0 {
		t.Fatalf("营销邮件应 is_bill=false：%+v", bill)
	}
}

// 防御性收敛：金额取整到分、非法日期清空、收入不占分类。
func TestParseBillNormalize(t *testing.T) {
	srv := billMockServer(t, func(string) string {
		return `{"is_bill":true,"type":"inc","amount_yuan":15000.005,"category":"乱写的","date":"不是日期","merchant":" 公司 "}`
	})
	defer srv.Close()
	k := newBillKimi(t, srv)
	bill, err := k.ParseBill(context.Background(), "hr@example.com", "工资", "正文", time.Now(), time.UTC, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bill.Type != "inc" || bill.AmountFen != 1500001 {
		t.Fatalf("金额取整不符：%+v", bill)
	}
	if bill.Date != "" {
		t.Fatalf("非法日期应清空，得到 %q", bill.Date)
	}
	if bill.Merchant != "公司" {
		t.Fatalf("商户名应去空白，得到 %q", bill.Merchant)
	}
}

func TestParseBillMissingKey(t *testing.T) {
	k := NewKimi("", "", "", time.Second)
	_, err := k.ParseBill(context.Background(), "a@b", "s", "b", time.Now(), time.UTC, nil)
	if err == nil || !strings.Contains(err.Error(), "未配置") {
		t.Fatalf("缺 Key 应如实报错，得到 %v", err)
	}
}
