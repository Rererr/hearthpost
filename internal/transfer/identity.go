package transfer

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// Identity は端末ごとの鍵と自己署名証明書。指紋（証明書 DER の SHA-256）が端末の ID になる。
type Identity struct {
	Cert        tls.Certificate
	Fingerprint string
}

const (
	keyFile  = "identity.key"
	certFile = "identity.crt"
)

// LoadOrCreateIdentity は dir に保存した鍵を読み、無ければ作って保存する。
func LoadOrCreateIdentity(dir string) (*Identity, error) {
	keyPath := filepath.Join(dir, keyFile)
	certPath := filepath.Join(dir, certFile)

	keyPEM, keyErr := os.ReadFile(keyPath)
	certPEM, certErr := os.ReadFile(certPath)
	switch {
	case keyErr == nil && certErr == nil:
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, fmt.Errorf("端末の鍵 %s を読めません（壊れている場合は %s と %s を削除すると作り直します。ペアリングはやり直しになります）: %w", dir, keyFile, certFile, err)
		}
		return newIdentity(cert), nil
	case errors.Is(keyErr, os.ErrNotExist) && errors.Is(certErr, os.ErrNotExist):
		// 初回起動。下で作る
	case keyErr == nil || certErr == nil:
		// 片方だけ作り直すと指紋が変わり、相手から「なりすまし」として拒否される。黙って作り直さない
		return nil, fmt.Errorf("%s に %s と %s の片方しかありません。両方を削除すると作り直します（ペアリングはやり直しになります）", dir, keyFile, certFile)
	case keyErr != nil && !errors.Is(keyErr, os.ErrNotExist):
		return nil, fmt.Errorf("端末の鍵を読めません: %w", keyErr)
	default:
		return nil, fmt.Errorf("端末の証明書を読めません: %w", certErr)
	}

	certPEM, keyPEM, err := generateCert()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("設定フォルダ %s を作れません: %w", dir, err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, fmt.Errorf("端末の鍵を保存できません: %w", err)
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return nil, fmt.Errorf("端末の証明書を保存できません: %w", err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("作った鍵を読み直せません: %w", err)
	}
	return newIdentity(cert), nil
}

func newIdentity(cert tls.Certificate) *Identity {
	return &Identity{Cert: cert, Fingerprint: fingerprint(cert.Certificate[0])}
}

func generateCert() (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("鍵を作れません: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, nil, fmt.Errorf("証明書の番号を作れません: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "hearthpost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(100, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("証明書を作れません: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("鍵を書き出せません: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// newNonce は確認コードを作るための 32 バイトの乱数を返す。
func newNonce() []byte {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read は Go 1.24 以降エラーを返さない
	return b
}

func commitment(nonce []byte) []byte {
	sum := sha256.Sum256(append([]byte("hearthpost-commit|"), nonce...))
	return sum[:]
}

// sessionCode は1回の接続で両方の画面に出す6桁の確認コードを作る。
// 接続した側が乱数のハッシュを先に送り、受けた側の乱数を見てから元の乱数を明かすので、
// 途中に割り込んだ端末は、両側のコードが一致する値を前もって選べない（成功は 100 万回に 1 回）。
// 2台の指紋だけから作ると、割り込む側が鍵を作り直して一致させられる。
func sessionCode(clientFP, serverFP string, clientNonce, serverNonce []byte) string {
	h := sha256.New()
	fmt.Fprintf(h, "hearthpost-code|%s|%s|", clientFP, serverFP)
	h.Write(clientNonce)
	h.Write(serverNonce)
	n := binary.BigEndian.Uint32(h.Sum(nil)[:4]) % 1_000_000
	return fmt.Sprintf("%03d %03d", n/1000, n%1000)
}
