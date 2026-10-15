package secretbox

import (
	"encoding/base64"
	"strings"
	"testing"
)

// 32 字节 base64。测试专用，不是真实密钥。
const testKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

func TestSealUnsealRoundTrip(t *testing.T) {
	box, err := FromKey(testKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, plain := range []string{"a", "bot-secret-value", "sk-1234567890", strings.Repeat("x", 500), "含中文的密钥"} {
		enc, err := box.Seal(plain)
		if err != nil {
			t.Fatalf("加密 %q 失败：%v", plain, err)
		}
		if enc == plain {
			t.Fatal("密文不应等于明文")
		}
		got, err := box.Unseal(enc)
		if err != nil {
			t.Fatalf("解密 %q 失败：%v", plain, err)
		}
		if got != plain {
			t.Fatalf("往返不一致：得到 %q，期望 %q", got, plain)
		}
	}
}

// 每次加密必须用新的 nonce，否则相同明文会产生相同密文，
// 等于泄露「这两个字段的值一样」。
func TestSealIsNonDeterministic(t *testing.T) {
	box, _ := FromKey(testKey)
	a, _ := box.Seal("同一个值")
	b, _ := box.Seal("同一个值")
	if a == b {
		t.Fatal("两次加密相同明文产生了相同密文，nonce 没有随机化")
	}
}

// 空串的语义是「这个字段没有值」，不是「加密后的空」。
func TestEmptyStringStaysEmpty(t *testing.T) {
	box, _ := FromKey(testKey)
	enc, err := box.Seal("")
	if err != nil || enc != "" {
		t.Fatalf("空串应当映射为空串：%v %q", err, enc)
	}
	plain, err := box.Unseal("")
	if err != nil || plain != "" {
		t.Fatalf("空密文应当映射为空串：%v %q", err, plain)
	}
}

// 换一把密钥必须解密失败 —— 否则等于「谁都能读」。
func TestUnsealRejectsWrongKey(t *testing.T) {
	box1, _ := FromKey(testKey)
	other := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	box2, _ := FromKey(other)

	enc, _ := box1.Seal("秘密")
	if _, err := box2.Unseal(enc); err == nil {
		t.Fatal("用错密钥竟然解密成功")
	}
}

// 篡改密文必须被 GCM 的认证标签发现。
func TestUnsealDetectsTampering(t *testing.T) {
	box, _ := FromKey(testKey)
	enc, _ := box.Seal("秘密")

	raw, _ := base64.StdEncoding.DecodeString(enc)
	raw[len(raw)-1] ^= 0xff // 翻转最后一字节
	tampered := base64.StdEncoding.EncodeToString(raw)

	if _, err := box.Unseal(tampered); err == nil {
		t.Fatal("篡改后的密文竟然通过校验")
	}
}

func TestUnsealRejectsMalformedInput(t *testing.T) {
	box, _ := FromKey(testKey)
	for _, bad := range []string{"not base64!!", "YWJj"} { // 第二个解码后长度不足
		if _, err := box.Unseal(bad); err == nil {
			t.Fatalf("非法密文 %q 应当报错", bad)
		}
	}
}

// 密钥长度不对要在构造时报错，且错误信息里不能回显密钥本身。
func TestFromKeyValidatesLength(t *testing.T) {
	short := base64.StdEncoding.EncodeToString([]byte("太短了"))
	_, err := FromKey(short)
	if err == nil {
		t.Fatal("长度不对的密钥应当报错")
	}
	if strings.Contains(err.Error(), short) {
		t.Fatal("错误信息不应回显密钥内容")
	}
	if !strings.Contains(err.Error(), "32") {
		t.Fatalf("错误信息应说明期望长度：%v", err)
	}
}

func TestFromKeyRejectsNonBase64(t *testing.T) {
	if _, err := FromKey("!!!不是 base64!!!"); err == nil {
		t.Fatal("非 base64 应当报错")
	}
}

// 未配置主密钥时返回 ErrNoKey，且 nil Box 上的操作也必须明确失败，
// 不能静默通过 —— 那会变成明文存储。
func TestNilBoxNeverSilentlyWorks(t *testing.T) {
	var box *Box
	if _, err := box.Seal("秘密"); err == nil {
		t.Fatal("nil Box 加密应当报错")
	}
	if _, err := box.Unseal("YQ=="); err == nil {
		t.Fatal("nil Box 解密应当报错")
	}
}

func TestGenerateKeyIsUsable(t *testing.T) {
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	box, err := FromKey(k)
	if err != nil {
		t.Fatalf("生成的密钥应当可用：%v", err)
	}
	enc, _ := box.Seal("v")
	if got, _ := box.Unseal(enc); got != "v" {
		t.Fatal("生成的密钥往返失败")
	}
}

// Open 在没有环境变量时应当返回 ErrNoKey（而不是别的错误），
// 调用方据此区分「没配」与「配错了」。
func TestOpenWithoutEnvReturnsNoKey(t *testing.T) {
	t.Setenv(EnvKey, "")
	_, err := Open()
	if err != ErrNoKey {
		t.Fatalf("应当返回 ErrNoKey，得到 %v", err)
	}
}

func TestOpenWithEnv(t *testing.T) {
	t.Setenv(EnvKey, testKey)
	box, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	enc, _ := box.Seal("v")
	if got, _ := box.Unseal(enc); got != "v" {
		t.Fatal("从环境变量构造的 Box 不可用")
	}
}
