package transfer

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/netip"
	"path"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	protocolVersion = 1
	maxFrame        = 16 << 20
	// MaxTextBytes は1回に送れるテキストの上限。
	MaxTextBytes = 1 << 20
	maxNameRunes = 64
)

type requestKind string

const (
	kindHello requestKind = "hello" // 相手の名前だけ聞く（IP 手入力で追加するとき）
	kindPair  requestKind = "pair"
	kindOffer requestKind = "offer"
)

// FileEntry は送る1項目。Path は送り手側のフォルダ構成を "/" 区切りの相対パスで表す。
type FileEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Dir  bool   `json:"dir,omitempty"`
}

type request struct {
	Version int         `json:"v"`
	Kind    requestKind `json:"kind"`
	Name    string      `json:"name"`
	Port    int         `json:"port"`
	Files   []FileEntry `json:"files,omitempty"`
	Text    string      `json:"text,omitempty"`
	// Commit は確認コード用の乱数のハッシュ。乱数そのものは相手の乱数を受け取ってから reveal で明かす。
	Commit []byte `json:"commit,omitempty"`
}

// challenge は受けた側の乱数。要求を断るときは、受けた側がこの位置に reply（Reason つき）を送るので、同じ名前で受ける。
type challenge struct {
	Nonce  []byte `json:"nonce"`
	Reason string `json:"reason,omitempty"`
}

type reveal struct {
	Nonce []byte `json:"nonce"`
}

// pairConfirm は、ペアリングを申し込んだ側の人が確認コードを見比べた結果。受けた側はこれを受け取ってから登録する。
type pairConfirm struct {
	OK bool `json:"ok"`
}

type reply struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason,omitempty"`
	Name     string `json:"name,omitempty"`
}

func writeFrame(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > maxFrame {
		return fmt.Errorf("送る情報が大きすぎます（%d バイト。上限 %d バイト）", len(b), maxFrame)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := w.Write(append(hdr[:], b...)); err != nil {
		return err
	}
	return nil
}

func readFrame(r io.Reader, v any) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxFrame {
		return fmt.Errorf("受け取った情報が大きすぎます（%d バイト）", n)
	}
	// 長さを先に確保すると、ヘッダだけ送る相手に上限まで確保させられるので、届いた分だけ伸ばす
	var buf bytes.Buffer
	if _, err := io.CopyN(&buf, r, int64(n)); err != nil {
		return err
	}
	return json.Unmarshal(buf.Bytes(), v)
}

// IsLANAddr は addr が LAN 内（プライベート・リンクローカル・ループバック）の住所かを返す。
func IsLANAddr(addr net.Addr) bool {
	ap, err := netip.ParseAddrPort(addr.String())
	if err != nil {
		return false
	}
	return IsLANIP(ap.Addr())
}

// tailscaleULA は Tailscale が IPv6 で使う範囲。ULA（fc00::/7）の中にあるが、LAN の外の端末につながる。
var tailscaleULA = netip.MustParsePrefix("fd7a:115c:a1e0::/48")

// IsLANIP は ip が LAN 内の住所かを返す。
// CGNAT（100.64.0.0/10）と Tailscale の IPv6 範囲は VPN の住所なので LAN に含めない。
func IsLANIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if tailscaleULA.Contains(ip) {
		return false
	}
	return ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLoopback()
}

// windowsForbidden は Windows のファイル名に使えない文字。Mac から送るときも同じ規則で弾き、
// 受け手の OS によって受け取れたり受け取れなかったりする差をなくす。
const windowsForbidden = `<>:"|?*\`

var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true,
}

func isWindowsReserved(base string) bool {
	base = strings.ToUpper(strings.TrimRight(base, " ")) // 「nul .txt」も NUL として扱われる
	if windowsReserved[base] {
		return true
	}
	if strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT") {
		switch base[3:] {
		case "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³":
			return true
		}
	}
	return false
}

// validateEntryPath は相手から来た相対パスが保存先の外を指さないこと、両 OS で作れる名前であることを確かめる。
func validateEntryPath(p string) error {
	if p == "" {
		return errors.New("空のパスがあります")
	}
	if path.IsAbs(p) || path.Clean(p) != p {
		return fmt.Errorf("パス %q は相対パスの正規形ではありません", p)
	}
	for _, part := range strings.Split(p, "/") {
		if err := validateName(part); err != nil {
			return fmt.Errorf("パス %q: %w", p, err)
		}
	}
	return nil
}

func validateName(name string) error {
	if name == "." || name == ".." || name == "" {
		return fmt.Errorf("名前 %q は使えません", name)
	}
	if i := strings.IndexAny(name, windowsForbidden); i >= 0 {
		return fmt.Errorf("名前 %q には Windows で使えない文字 %q が含まれます", name, name[i])
	}
	for _, r := range name {
		if r < 0x20 {
			return fmt.Errorf("名前 %q に制御文字が含まれます", name)
		}
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return fmt.Errorf("名前 %q は末尾がドットか空白のため Windows で作れません", name)
	}
	base, _, _ := strings.Cut(name, ".")
	if isWindowsReserved(base) {
		return fmt.Errorf("名前 %q は Windows の予約名のため使えません", name)
	}
	return nil
}

// checkOffer は受け取る一覧全体を確かめ、パスを NFC にそろえて、合計サイズを返す。
// Mac は濁点を分けた形（NFD）で名前を持つことがあり、そのまま Windows に渡すと別の名前として扱われるため NFC にする。
// Mac・Windows とも既定で大文字小文字を区別しないので、大文字小文字だけが違う名前の組も断る。
func checkOffer(files []FileEntry, text string) (int64, error) {
	if len(files) == 0 && text == "" {
		return 0, errors.New("送る内容が空です")
	}
	if len(text) > MaxTextBytes {
		return 0, fmt.Errorf("テキストが大きすぎます（%d バイト。上限 %d バイト）", len(text), MaxTextBytes)
	}
	// 小文字化（ToLower）では ß と ss、ſ と s のように、ファイルシステムが同じとみなす組を見逃すので、case folding で比べる
	fold := cases.Fold()
	key := func(p string) string { return fold.String(p) }
	dirs := map[string]string{}  // 比較用のパス → 実際のパス
	plain := map[string]string{} // ファイルについて同上
	sameName := func(prev, p string) error {
		return fmt.Errorf("%q と %q は大文字小文字の違いしかないため、同じ名前として扱われます", prev, p)
	}
	claim := func(m map[string]string, p string) error {
		k := key(p)
		if prev, ok := m[k]; ok && prev != p {
			return sameName(prev, p)
		}
		m[k] = p
		return nil
	}
	var total int64
	for i := range files {
		f := &files[i]
		f.Path = norm.NFC.String(f.Path)
		if err := validateEntryPath(f.Path); err != nil {
			return 0, err
		}
		if f.Size < 0 || (f.Dir && f.Size != 0) {
			return 0, fmt.Errorf("パス %q のサイズ %d が不正です", f.Path, f.Size)
		}
		if total > math.MaxInt64-f.Size {
			return 0, errors.New("合計サイズが大きすぎます")
		}
		total += f.Size

		parts := strings.Split(f.Path, "/")
		for j := 1; j < len(parts); j++ {
			parent := strings.Join(parts[:j], "/")
			if prev, ok := plain[key(parent)]; ok {
				return 0, fmt.Errorf("%q はファイルなのに、その中に %q があります", prev, f.Path)
			}
			if err := claim(dirs, parent); err != nil {
				return 0, err
			}
		}
		k := key(f.Path)
		if f.Dir {
			if _, ok := plain[k]; ok {
				return 0, fmt.Errorf("パス %q がファイルとフォルダの両方にあります", f.Path)
			}
			if err := claim(dirs, f.Path); err != nil {
				return 0, err
			}
			continue
		}
		if _, ok := dirs[k]; ok {
			return 0, fmt.Errorf("パス %q がファイルとフォルダの両方にあります", f.Path)
		}
		if prev, ok := plain[k]; ok {
			if prev != f.Path {
				return 0, sameName(prev, f.Path)
			}
			return 0, fmt.Errorf("パス %q が重複しています", f.Path)
		}
		plain[k] = f.Path
	}
	return total, nil
}

// cleanName は相手が名乗った端末名から制御文字を除き、長さを抑える。画面と設定ファイルに出すため。
func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxNameRunes {
		s = string(r[:maxNameRunes])
	}
	if s == "" {
		return "名前のない端末"
	}
	return s
}
