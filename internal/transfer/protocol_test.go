package transfer

import (
	"fmt"
	"math"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateEntryPath(t *testing.T) {
	ok := []string{"a.txt", "フォルダ/日本語.txt", "a/b/c", ".hidden", "COM10", "console.txt"}
	ng := []string{"", "../x", "a/../b", "/abs", "a//b", "a/", "./a", `a\b`, "C:x", "CON", "con.txt", "a.", "a ", "a\x01b", `a"b`,
		"COM¹", "lpt³.log", "CONIN$", "nul .txt"}
	for _, p := range ok {
		if err := validateEntryPath(p); err != nil {
			t.Errorf("%q は受け取れるはず: %v", p, err)
		}
	}
	for _, p := range ng {
		if err := validateEntryPath(p); err == nil {
			t.Errorf("%q は断るはず", p)
		}
	}
}

func TestCheckOffer(t *testing.T) {
	ng := map[string][]FileEntry{
		"大文字小文字だけ違う名前":     {{Path: "A.txt"}, {Path: "a.txt"}},
		"大文字小文字だけ違う上位フォルダ": {{Path: "A", Dir: true}, {Path: "a/x.txt", Size: 1}},
		"ファイルの中にファイル":      {{Path: "a", Size: 1}, {Path: "a/b", Size: 1}},
		"フォルダとファイルが同じ名前":   {{Path: "a/b", Size: 1}, {Path: "a", Size: 1}},
		"合計サイズのあふれ":        {{Path: "x", Size: math.MaxInt64}, {Path: "y", Size: math.MaxInt64}, {Path: "z", Size: 2}},
		"負のサイズ":            {{Path: "x", Size: -1}},
		"ß と ss":           {{Path: "straße.txt", Size: 1}, {Path: "strasse.txt", Size: 1}},
		"ſ と s のフォルダ":      {{Path: "ſub/x", Size: 1}, {Path: "sub/y", Size: 1}},
	}
	for name, files := range ng {
		if _, err := checkOffer(files, ""); err == nil {
			t.Errorf("%s は断る", name)
		}
	}
	if _, err := checkOffer(nil, strings.Repeat("x", MaxTextBytes+1)); err == nil {
		t.Fatal("上限を超えるテキストは断る")
	}
	if _, err := checkOffer(nil, ""); err == nil {
		t.Fatal("空の送信は断る")
	}

	nfd := "か\u3099.txt" // Mac が持つ濁点を分けた形（NFD）
	files := []FileEntry{{Path: "dir", Dir: true}, {Path: "dir/" + nfd, Size: 3}}
	total, err := checkOffer(files, "")
	if err != nil || total != 3 {
		t.Fatal(err)
	}
	if files[1].Path != "dir/が.txt" {
		t.Fatalf("名前は NFC にそろえる: %q", files[1].Path)
	}
}

func TestIsLANIP(t *testing.T) {
	cases := map[string]bool{
		"192.168.1.10": true, "10.0.0.1": true, "172.16.5.5": true, "169.254.1.1": true, "127.0.0.1": true,
		"fd00::1": true, "fe80::1": true, "::ffff:192.168.0.1": true,
		"100.64.1.1": false, "8.8.8.8": false, "172.32.0.1": false, "2001:db8::1": false,
		"fd7a:115c:a1e0::1": false, // Tailscale の IPv6
	}
	for s, want := range cases {
		if got := IsLANIP(netip.MustParseAddr(s)); got != want {
			t.Errorf("%s: got %v want %v", s, got, want)
		}
	}
}

func TestSessionCode(t *testing.T) {
	a, b := strings.Repeat("1", 64), strings.Repeat("2", 64)
	n1, n2 := newNonce(), newNonce()
	c := sessionCode(a, b, n1, n2)
	if len(c) != 7 || c[3] != ' ' {
		t.Fatalf("「123 456」の形: %q", c)
	}
	if c == sessionCode(a, b, newNonce(), n2) || c == sessionCode(a, strings.Repeat("3", 64), n1, n2) {
		t.Fatal("乱数か相手が違えばコードも違う")
	}
}

func TestParseBeacon(t *testing.T) {
	self := strings.Repeat("a", 64)
	other := strings.Repeat("b", 64)
	lan := netip.MustParseAddr("192.168.1.2")
	mk := func(app, fp string, port int) []byte {
		return fmt.Appendf(nil, `{"app":%q,"v":1,"fp":%q,"name":"x","port":%d}`, app, fp, port)
	}
	if _, ok := parseBeacon(mk("hearthpost", other, 47821), lan, self); !ok {
		t.Fatal("LAN 内の相手の知らせは受け取る")
	}
	if _, ok := parseBeacon(mk("hearthpost", self, 47821), lan, self); ok {
		t.Fatal("自分の知らせは捨てる")
	}
	if _, ok := parseBeacon(mk("other", other, 47821), lan, self); ok {
		t.Fatal("他のアプリのパケットは捨てる")
	}
	if _, ok := parseBeacon(mk("hearthpost", other, 47821), netip.MustParseAddr("8.8.8.8"), self); ok {
		t.Fatal("LAN の外からの知らせは捨てる")
	}
	if _, ok := parseBeacon(mk("hearthpost", other, 0), lan, self); ok {
		t.Fatal("不正なポートは捨てる")
	}
}

func TestCleanName(t *testing.T) {
	if got := cleanName(" a\x00b\nc "); got != "abc" {
		t.Fatalf("制御文字を除く: %q", got)
	}
	if got := cleanName(strings.Repeat("あ", 100)); len([]rune(got)) != maxNameRunes {
		t.Fatalf("長さを抑える: %d", len([]rune(got)))
	}
	if cleanName("\x01") != "名前のない端末" {
		t.Fatal("空になったら代わりの名前")
	}
}

func TestReceiveFailureRemovesStaging(t *testing.T) {
	dir := t.TempDir()
	files := []FileEntry{{Path: "a.txt", Size: 3}, {Path: "b.txt", Size: 10}}
	_, err := receiveFiles(strings.NewReader("abc12"), dir, files, nil)
	if err == nil || !strings.Contains(err.Error(), "b.txt") {
		t.Fatalf("途中で切れたらどのファイルか示して失敗する: %v", err)
	}
	if got := listDir(t, dir); len(got) != 0 {
		t.Fatalf("途中で切れたら保存先に何も残さない: %v", got)
	}
}

func TestUniquePath(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"v1.2", ".env", "a.txt"} {
		writeFile(t, filepath.Join(dir, n), nil)
	}
	cases := []struct {
		name  string
		isDir bool
		want  string
	}{
		{"v1.2", true, "v1.2 (1)"},
		{".env", false, ".env (1)"},
		{"a.txt", false, "a (1).txt"},
		{"new.txt", false, "new.txt"},
	}
	for _, c := range cases {
		got, err := uniquePath(dir, c.name, c.isDir)
		if err != nil || filepath.Base(got) != c.want {
			t.Errorf("%s: got %q want %q (%v)", c.name, filepath.Base(got), c.want, err)
		}
	}
}

func TestIdentityAndStorePersist(t *testing.T) {
	dir := t.TempDir()
	id1, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if id1.Fingerprint != id2.Fingerprint {
		t.Fatal("再起動しても同じ鍵を使う（変わるとペアリングが切れる）")
	}
	if err := os.Remove(filepath.Join(dir, keyFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateIdentity(dir); err == nil || !strings.Contains(err.Error(), "片方しかありません") {
		t.Fatalf("鍵と証明書の片方だけを黙って作り直さない: %v", err)
	}

	path := filepath.Join(dir, "config.json")
	st, _ := OpenStore(path, "def", dir)
	if err := st.AddPeer(PairedPeer{Fingerprint: "f", Name: "n"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetName("新しい名前"); err != nil {
		t.Fatal(err)
	}
	st2, err := OpenStore(path, "def", dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st2.Paired("f"); !ok || st2.Name() != "新しい名前" {
		t.Fatal("設定が保存されていない")
	}
	if err := st.SetName(""); err == nil {
		t.Fatal("空の名前は断る")
	}
	if err := st.SetSaveDir(filepath.Join(dir, "none")); err == nil {
		t.Fatal("存在しない保存先は断る")
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(path, "def", dir); err == nil || !strings.Contains(err.Error(), "壊れています") {
		t.Fatalf("壊れた設定は理由つきで失敗する: %v", err)
	}
}
