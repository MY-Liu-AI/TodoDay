package mailimport

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"
)

// tarpitListener 完成 TCP+TLS 握手后什么都不发——模拟被封禁时
// QQ 的行为（greeting 永远不到来）。测试自签 CA 只绑 127.0.0.1。
func tarpitListener(t *testing.T) (addr string, root *x509.Certificate, cleanup func()) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:              []string{"localhost"},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	rootCert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsLn := tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
	go func() {
		for {
			conn, err := tlsLn.Accept()
			if err != nil {
				return
			}
			// 先完成 TLS 握手（QQ 封禁时握手正常），然后挂起：
			// 永不发送 IMAP greeting。
			go func(c *tls.Conn) {
				_ = c.Handshake()
				select {}
			}(conn.(*tls.Conn))
		}
	}()
	return ln.Addr().String(), rootCert, func() { tlsLn.Close() }
}

func TestOpenHardTimeoutAgainstTarpit(t *testing.T) {
	addr, root, cleanup := tarpitListener(t)
	defer cleanup()

	pool := x509.NewCertPool()
	pool.AddCert(root)
	tr := &Transport{
		Addr:      addr,
		TLSConfig: &tls.Config{RootCAs: pool, ServerName: "localhost"},
	}
	start := time.Now()
	_, err := tr.Open(context.Background(), "a@b.c", "secret", nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("tarpit：Open 应该失败，却成功了")
	}
	t.Logf("tarpit Open 失败（符合预期）：%v，耗时 %v", err, elapsed)
	if elapsed > 45*time.Second {
		t.Fatalf("硬超时未生效：耗时 %v（应 ≤ 30s 建连硬超时 + 余量）", elapsed)
	}
}
