package wecom

import (
	"strings"
	"testing"
)

// 官方文档给出的回调测试参数。这组值来自企业微信官方的加解密示例，
// 用它做互操作测试：只有实现与微信服务器完全一致时才能通过。
//
// 注意 msg_signature 是四个值字典序排序后拼接取 SHA1：
//
//	排序后 = 1409659589, 263014780, echostr, QDG6eK
//
// 只做「自己加密再自己解密」的往返测试是不够的 —— 那只能证明自洽，
// 证明不了与微信服务器一致。
const (
	testToken  = "QDG6eK"
	testAESKey = "jWmYm7qr5nMoAUwZRjGtBxmz3KA1tkAj3ykkR6q2B2C"
	testCorpID = "wx5823bf96d3bd56c7"
	testTimest = "1409659589"
	testNonce  = "263014780"
	testEcho   = "P9nAzCzyDtyTWESHep1vC5X9xho/qYX3Zpb4yKa9SKld1DsH3Iyt3tP3zNdtp+4RPcs8TgAE7OaBO+FZXvnaqQ=="
	testSig    = "5c45ff5e21c57e6ad56bac8758b79b1d9ac89fd3"
)

func TestSignatureMatchesOfficialVector(t *testing.T) {
	got := Signature(testToken, testTimest, testNonce, testEcho)
	if got != testSig {
		t.Fatalf("签名与官方向量不符：\n得到 %s\n期望 %s", got, testSig)
	}
}

// 官方向量同时验证了解密链路：AES-256-CBC、IV=密钥前 16 字节、
// PKCS#7（32 字节块）、明文布局，以及尾部的 receiveid。
func TestDecryptOfficialEchoStr(t *testing.T) {
	key, err := DecodeAESKey(testAESKey)
	if err != nil {
		t.Fatalf("解析官方向量的密钥失败：%v", err)
	}
	msg, receiveID, err := Decrypt(key, testEcho)
	if err != nil {
		t.Fatalf("解密官方 echostr 失败：%v", err)
	}
	if strings.TrimSpace(msg) == "" {
		t.Fatal("解密结果为空")
	}
	if receiveID != testCorpID {
		t.Fatalf("receiveid 不符：得到 %q，期望 %q", receiveID, testCorpID)
	}
	t.Logf("官方 echostr 明文：%s", msg)
}

func TestDecodeAESKey(t *testing.T) {
	key, err := DecodeAESKey(testAESKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 {
		t.Fatalf("密钥应为 32 字节，得到 %d", len(key))
	}
	if _, err := DecodeAESKey("太短"); err == nil {
		t.Fatal("长度不对的密钥应当报错")
	}
	if _, err := DecodeAESKey(strings.Repeat("!", 43)); err == nil {
		t.Fatal("非 base64 的密钥应当报错")
	}
}

// 往返测试覆盖填充边界：长度正好是 32 的倍数时要补满一整块。
func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, err := DecodeAESKey(testAESKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, 1, 15, 16, 31, 32, 33, 64, 100, 1000} {
		original := strings.Repeat("a", n)
		cipherText, err := Encrypt(key, original, testCorpID)
		if err != nil {
			t.Fatalf("长度 %d 加密失败：%v", n, err)
		}
		got, receiveID, err := Decrypt(key, cipherText)
		if err != nil {
			t.Fatalf("长度 %d 解密失败：%v", n, err)
		}
		if got != original {
			t.Fatalf("长度 %d 往返不一致：得到 %q", n, got)
		}
		if receiveID != testCorpID {
			t.Fatalf("长度 %d receiveid 不一致：得到 %q", n, receiveID)
		}
	}
}

// 用错密钥必须解密失败或得到错的 receiveid，绝不能悄悄通过。
func TestDecryptRejectsWrongKey(t *testing.T) {
	right, _ := DecodeAESKey(testAESKey)
	wrong, _ := DecodeAESKey("GCbJYTAHnfqK5YPC75L01OxPret6JMLm1o6kuBzuTgK")
	cipherText, err := Encrypt(right, "<xml>hi</xml>", testCorpID)
	if err != nil {
		t.Fatal(err)
	}
	msg, receiveID, err := Decrypt(wrong, cipherText)
	if err == nil && receiveID == testCorpID {
		t.Fatalf("用错密钥竟然通过了校验：msg=%q receiveID=%q", msg, receiveID)
	}
}

func TestDecryptRejectsMalformedInput(t *testing.T) {
	key, _ := DecodeAESKey(testAESKey)
	for _, bad := range []string{"", "not base64!!", "YWJj"} {
		if _, _, err := Decrypt(key, bad); err == nil {
			t.Fatalf("非法密文 %q 应当报错", bad)
		}
	}
}

func TestSignatureMatches(t *testing.T) {
	if !SignatureMatches("abc", "abc") {
		t.Fatal("相同签名应当匹配")
	}
	if SignatureMatches("abc", "abd") {
		t.Fatal("不同签名不应当匹配")
	}
	if SignatureMatches("abc", "abcd") {
		t.Fatal("长度不同不应当匹配")
	}
}

// 签名与参数顺序无关：四个值先排序再拼接。
func TestSignatureIsOrderIndependent(t *testing.T) {
	a := Signature("t", "1", "n", "e")
	b := Signature("e", "n", "t", "1")
	if a != b {
		t.Fatalf("签名应当与参数顺序无关：%s vs %s", a, b)
	}
}

// 填充规则（PKCS#7，块大小 32）：补到下一个 32 的倍数；长度正好整除时补满
// 一整块。这是与官方 PKCS7Encoder 对齐的关键点 —— 用 16 会通不过微信校验。
//
// 注意 31 字节补 1 个字节（32 字节），只有 32 的整倍数才补满一整块。
func TestPadBlockSize(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 32}, {1, 32}, {31, 32}, {32, 64}, {33, 64}, {64, 96}, {65, 96},
	}
	for _, tc := range cases {
		padded := pad(make([]byte, tc.in))
		if len(padded) != tc.want {
			t.Errorf("长度 %d 应补到 %d，得到 %d", tc.in, tc.want, len(padded))
			continue
		}
		// 填充值必须等于实际补入的字节数，且落在 1..32。
		n := int(padded[len(padded)-1])
		if added := len(padded) - tc.in; n != added {
			t.Errorf("长度 %d 的填充值 %d 与实际补入的 %d 字节不符", tc.in, n, added)
		}
		if n < 1 || n > blockSize {
			t.Errorf("长度 %d 的填充值 %d 越界", tc.in, n)
		}
		// 去填充必须还原，且长度归零的情况（全填充块）也要正确。
		restored, err := unpad(padded)
		if err != nil {
			t.Errorf("长度 %d 去填充失败：%v", tc.in, err)
			continue
		}
		if len(restored) != tc.in {
			t.Errorf("长度 %d 去填充后应为 %d，得到 %d", tc.in, tc.in, len(restored))
		}
	}
}

// 去填充要拒绝非法值，否则损坏的密文可能被当成有效数据放过去。
func TestUnpadRejectsInvalidValues(t *testing.T) {
	if _, err := unpad(nil); err == nil {
		t.Error("空数据应当报错")
	}
	// 填充值 0 非法。
	if _, err := unpad([]byte{1, 2, 0}); err == nil {
		t.Error("填充值 0 应当报错")
	}
	// 填充值超过块大小非法。
	if _, err := unpad([]byte{1, 2, 33}); err == nil {
		t.Error("填充值 33 应当报错")
	}
	// 填充值大于数据长度非法。
	if _, err := unpad([]byte{5}); err == nil {
		t.Error("填充值大于数据长度应当报错")
	}
}
