// Package wecom 实现企业微信自建应用的「接收消息」回调协议：
// 签名校验、AES 加解密、以及主动发消息。
//
// 这里是最容易出错的一段，几个必须守住的点（错一个就整个接不通）：
//
//   - 群机器人 webhook 只能往外推，不能收消息；要收消息只能用自建应用回调。
//   - 签名是「四个值字典序排序后拼接」再 SHA1，GET 用 echostr 参与，
//     POST 用 body 里的 Encrypt 参与 —— 两者用错了值就永远校验不通过。
//   - URL 验证必须返回**明文** echostr，不能加引号、BOM 或换行。
//   - 填充用的是 32 字节块（官方 PKCS7Encoder 的 block_size 是 32，不是 16）。
//   - 明文尾部带 receiveid，必须校验，否则等于接受任意来源的伪造密文。
package wecom

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// blockSize 是官方实现里 PKCS7Encoder 的块大小。它比 AES 的 16 字节块大，
// 且长度正好是 32 的倍数时补满一整块（32 字节）。
const blockSize = 32

// Signature 计算消息签名：把 token、timestamp、nonce 和密文
// （GET 时是 echostr，POST 时是 Encrypt）字典序排序后拼接，取 SHA1 十六进制小写。
func Signature(token, timestamp, nonce, encrypt string) string {
	parts := []string{token, timestamp, nonce, encrypt}
	sort.Strings(parts)
	sum := sha1.Sum([]byte(strings.Join(parts, "")))
	return fmt.Sprintf("%x", sum)
}

// SignatureMatches 以常量时间比较签名，避免泄露逐字节的匹配进度。
func SignatureMatches(expected, actual string) bool {
	return subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}

// DecodeAESKey 把 43 字符的 EncodingAESKey 还原成 32 字节 AES-256 密钥。
// 官方要求补一个 '=' 才能 base64 解码。
func DecodeAESKey(encodingAESKey string) ([]byte, error) {
	if len(encodingAESKey) != 43 {
		return nil, fmt.Errorf("EncodingAESKey 必须是 43 个字符，当前 %d 个", len(encodingAESKey))
	}
	key, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
	if err != nil {
		return nil, fmt.Errorf("EncodingAESKey 不是合法 base64：%w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("解码后的 AES 密钥必须是 32 字节，当前 %d 字节", len(key))
	}
	return key, nil
}

// Decrypt 解密回调密文，返回消息明文与其中的 receiveid。
// receiveid 必须由调用方与自己的 CorpID 比对，这里不做判断。
func Decrypt(key []byte, encrypted string) (msg, receiveID string, err error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", "", fmt.Errorf("密文不是合法 base64：%w", err)
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return "", "", fmt.Errorf("密文长度 %d 不是 AES 块的整数倍", len(ciphertext))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", "", fmt.Errorf("构造 AES 失败：%w", err)
	}
	// IV 取密钥前 16 字节，这是官方约定。
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, key[:aes.BlockSize]).CryptBlocks(plain, ciphertext)

	plain, err = unpad(plain)
	if err != nil {
		return "", "", err
	}
	// 明文结构：random(16) + msg_len(4, 大端) + msg + receiveid
	if len(plain) < 20 {
		return "", "", fmt.Errorf("解密后的明文只有 %d 字节，不足 20 字节的头部", len(plain))
	}
	msgLen := int(binary.BigEndian.Uint32(plain[16:20]))
	if msgLen < 0 || msgLen > len(plain)-20 {
		return "", "", fmt.Errorf("明文声明的长度 %d 超出实际可用 %d 字节", msgLen, len(plain)-20)
	}
	return string(plain[20 : 20+msgLen]), string(plain[20+msgLen:]), nil
}

// Encrypt 加密回复内容，生成与官方一致的密文。
func Encrypt(key []byte, msg, receiveID string) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("生成随机前缀失败：%w", err)
	}
	body := []byte(msg)
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(body)))

	plain := bytes.Join([][]byte{random, length, body, []byte(receiveID)}, nil)
	plain = pad(plain)

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("构造 AES 失败：%w", err)
	}
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(out, plain)
	return base64.StdEncoding.EncodeToString(out), nil
}

// pad 按 32 字节块做 PKCS#7 填充。长度正好整除时补满一整块。
func pad(data []byte) []byte {
	n := blockSize - len(data)%blockSize
	return append(data, bytes.Repeat([]byte{byte(n)}, n)...)
}

// unpad 去除填充。填充值必须落在 1..32，否则视为密文损坏。
func unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("解密结果为空")
	}
	n := int(data[len(data)-1])
	if n < 1 || n > blockSize || n > len(data) {
		return nil, fmt.Errorf("填充字节 %d 非法", n)
	}
	return data[:len(data)-n], nil
}

// ReadAllLimited 读取请求体并限制大小，防止超大请求打满内存。
func ReadAllLimited(r io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, limit))
}
