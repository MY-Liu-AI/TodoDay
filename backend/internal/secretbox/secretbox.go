// Package secretbox 负责把用户填写的第三方凭据加密后落库。
//
// 这是仓库里第一处需要「解密回明文并交给外部服务」的凭据 —— 助手的
// Bot Secret 要发给企业微信，Kimi Key 要发给 Moonshot，都必须还原成明文。
// 现有的 bcrypt 只做单向密码哈希，无法复用。
//
// 主密钥来自环境变量 TODO_CONFIG_KEY（base64 编码的 32 字节）。缺失时不
// 阻止服务启动（其他功能不该被牵连），但**保存密钥会被拒绝** —— 绝不
// 退化成明文存储。
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

// EnvKey 是主密钥所在的环境变量名。错误信息里只提这个名字，不打印值。
const EnvKey = "TODO_CONFIG_KEY"

// ErrNoKey 表示未配置主密钥。调用方据此给出「请设置 TODO_CONFIG_KEY」的提示。
var ErrNoKey = errors.New("未配置主密钥")

// Box 用一把主密钥做 AES-256-GCM 加密。
type Box struct {
	aead cipher.AEAD
}

// Open 从环境变量读取主密钥并构造 Box。
//
// 未配置时返回 (nil, ErrNoKey)：调用方应当据此禁用「保存密钥」，
// 但让进程继续跑。
func Open() (*Box, error) {
	raw := strings.TrimSpace(os.Getenv(EnvKey))
	if raw == "" {
		return nil, ErrNoKey
	}
	return FromKey(raw)
}

// FromKey 用给定的 base64 密钥构造 Box。便于测试直接传密钥。
func FromKey(base64Key string) (*Box, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(base64Key))
	if err != nil {
		return nil, fmt.Errorf("%s 不是合法 base64：%w", EnvKey, err)
	}
	if len(key) != 32 {
		// 只报长度，不回显密钥本身。
		return nil, fmt.Errorf("%s 解码后必须是 32 字节（AES-256），当前 %d 字节", EnvKey, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("构造 AES 失败：%w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("构造 GCM 失败：%w", err)
	}
	return &Box{aead: aead}, nil
}

// Seal 加密明文，返回 base64(nonce ‖ 密文 ‖ tag)。
// 空串返回空串：语义是「这个字段没有值」，不需要密文。
func (b *Box) Seal(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if b == nil {
		return "", ErrNoKey
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("生成随机数失败：%w", err)
	}
	// nonce 前置，解密时按同一长度切分。
	out := b.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Unseal 解密 Seal 的产物。空串返回空串。
//
// 解密失败说明主密钥换了或数据损坏，两者都无法自行恢复，所以直接把错误
// 交给调用方 —— 它要能告诉用户「需要重新填写」。
func (b *Box) Unseal(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	if b == nil {
		return "", ErrNoKey
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("密文不是合法 base64：%w", err)
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("密文长度不足")
	}
	plain, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", errors.New("解密失败：主密钥可能已更换")
	}
	return string(plain), nil
}

// GenerateKey 生成一把新的 base64 主密钥，供部署时写入环境变量。
func GenerateKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}
