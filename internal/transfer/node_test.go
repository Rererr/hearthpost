package transfer

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakePrompter struct {
	mu       sync.Mutex
	decision Decision
	delay    time.Duration // 答えるまで待つ時間
	block    bool          // true なら ctx が切れるまで答えず、切れたあとに「承認」を返す
	prompts  []Prompt
	closed   chan struct{}
}

func (f *fakePrompter) Ask(ctx context.Context, p Prompt) Decision {
	f.mu.Lock()
	f.prompts = append(f.prompts, p)
	d, block, delay := f.decision, f.block, f.delay
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		if f.closed != nil {
			close(f.closed)
		}
		return Decision{Accept: true, Pair: true}
	}
	time.Sleep(delay)
	return d
}

func (f *fakePrompter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.prompts)
}

func (f *fakePrompter) last() Prompt {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prompts[len(f.prompts)-1]
}

type fakeEvents struct {
	mu       sync.Mutex
	texts    []string
	progress []Progress
	errs     []string
}

func (e *fakeEvents) PeersChanged() {}
func (e *fakeEvents) Progress(p Progress) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.progress = append(e.progress, p)
}
func (e *fakeEvents) TextReceived(from, text string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.texts = append(e.texts, from+":"+text)
}
func (e *fakeEvents) Error(msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.errs = append(e.errs, msg)
}

func (e *fakeEvents) last(dir Direction) Progress {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := len(e.progress) - 1; i >= 0; i-- {
		if e.progress[i].Direction == dir {
			return e.progress[i]
		}
	}
	return Progress{}
}

func (e *fakeEvents) codes(dir Direction) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, p := range e.progress {
		if p.Direction == dir && p.Code != "" {
			out = append(out, p.Code)
		}
	}
	return out
}

type testNode struct {
	*Node
	prompter *fakePrompter
	events   *fakeEvents
	saveDir  string
}

func newTestNode(t *testing.T, name string, opts ...func(*Options)) *testNode {
	t.Helper()
	dir := t.TempDir()
	id, err := LoadOrCreateIdentity(filepath.Join(dir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	saveDir := filepath.Join(dir, "save")
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := OpenStore(filepath.Join(dir, "config", "config.json"), name, saveDir)
	if err != nil {
		t.Fatal(err)
	}
	tn := &testNode{prompter: &fakePrompter{}, events: &fakeEvents{}, saveDir: saveDir}
	o := Options{Identity: id, Store: st, Prompter: tn.prompter, Events: tn.events}
	for _, f := range opts {
		f(&o)
	}
	tn.Node = NewNode(o)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := tn.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return tn
}

// same は、ペアリングを申し込んだ側の人が「同じ数字です」を押したことにする。
func same(context.Context) bool { return true }

func withIdle(d time.Duration) func(*Options) { return func(o *Options) { o.IdleTimeout = d } }

func (tn *testNode) addr() string { return fmt.Sprintf("127.0.0.1:%d", tn.Port()) }

// connect は a から b を手入力で追加する（テストでは端末探しを動かさないため）。
func connect(t *testing.T, a, b *testNode) {
	t.Helper()
	p, err := a.AddManual(context.Background(), b.addr())
	if err != nil {
		t.Fatal(err)
	}
	if p.Fingerprint != b.Fingerprint() {
		t.Fatalf("追加した相手の指紋が違う: %s", p.Fingerprint)
	}
}

func writeFile(t *testing.T, p string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel != "." {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func waitPrompt(t *testing.T, tn *testNode) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for tn.prompter.count() == 0 {
		select {
		case <-deadline:
			t.Fatal("受け手に問い合わせが出ない")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestSendFilesAndFolderToUnpairedPeerAfterApproval(t *testing.T) {
	a, b := newTestNode(t, "Mac"), newTestNode(t, "Windows")
	connect(t, a, b)
	b.prompter.decision = Decision{Accept: true}

	src := t.TempDir()
	big := bytes.Repeat([]byte("0123456789"), 300_000) // 3MB。複数回の読み書きにまたがる大きさ
	writeFile(t, filepath.Join(src, "memo.txt"), []byte("こんにちは"))
	writeFile(t, filepath.Join(src, "photos", "a.bin"), big)
	writeFile(t, filepath.Join(src, "photos", "sub", "b.txt"), []byte("b"))
	writeFile(t, filepath.Join(src, "photos", ".DS_Store"), []byte("mac"))
	writeFile(t, filepath.Join(src, "photos", "Icon\r"), []byte("icon"))
	if err := os.MkdirAll(filepath.Join(src, "photos", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := a.Send(context.Background(), b.Fingerprint(), []string{filepath.Join(src, "memo.txt"), filepath.Join(src, "photos")}, ""); err != nil {
		t.Fatal(err)
	}

	if got := b.prompter.count(); got != 1 {
		t.Fatalf("未ペアリングの相手からは承認を1回求める: got %d", got)
	}
	pr := b.prompter.last()
	if pr.Kind != PromptOffer || pr.PeerName != "Mac" || pr.FileCount != 3 || pr.TotalSize != int64(len(big)+len("こんにちは")+1) {
		t.Fatalf("承認の問い合わせの中身が違う: %+v", pr)
	}
	if codes := a.events.codes(Sending); len(codes) == 0 || codes[0] != pr.Code || len(pr.Code) != 7 {
		t.Fatalf("送り手と受け手に同じ確認コードが出る: 送り手 %v 受け手 %q", codes, pr.Code)
	}
	if d := time.Until(time.UnixMilli(pr.ExpiresAt)); d < 50*time.Second || d > DefaultPromptTimeout {
		t.Fatalf("問い合わせの期限は届いた時点から %s: 残り %s", DefaultPromptTimeout, d)
	}

	want := []string{"memo.txt", "photos", "photos/a.bin", "photos/empty", "photos/sub", "photos/sub/b.txt"}
	if got := listDir(t, b.saveDir); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("保存された構成が違う（.DS_Store と Icon\\r は送らない）:\n got %v\nwant %v", got, want)
	}
	got, _ := os.ReadFile(filepath.Join(b.saveDir, "photos", "a.bin"))
	if !bytes.Equal(got, big) {
		t.Fatal("大きいファイルの中身が一致しない")
	}
	if p := a.events.last(Sending); p.State != StateDone || p.Done != p.Total {
		t.Fatalf("送り手の最後の進捗が完了でない: %+v", p)
	}
	if p := b.events.last(Receiving); p.State != StateDone || len(p.Saved) != 2 {
		t.Fatalf("受け手の最後の進捗が完了でない: %+v", p)
	}
	if _, ok := b.store.Paired(a.Fingerprint()); ok {
		t.Fatal("ペアリングを選ばずに承認したのにペアリングされた")
	}
}

func TestApproveWithPairingRegistersOnlyReceiver(t *testing.T) {
	a, b := newTestNode(t, "Mac"), newTestNode(t, "Windows")
	connect(t, a, b)
	b.prompter.decision = Decision{Accept: true, Pair: true}

	if err := a.Send(context.Background(), b.Fingerprint(), nil, "一回目"); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.store.Paired(a.Fingerprint()); !ok {
		t.Fatal("受け手が送り手を登録していない")
	}
	if _, ok := a.store.Paired(b.Fingerprint()); ok {
		t.Fatal("送り手の人は受け手から自動で受け取ることに同意していないので、送り手側には登録しない")
	}

	b.prompter.decision = Decision{} // 次に聞かれたら断る。聞かれなければ受け取れる
	if err := a.Send(context.Background(), b.Fingerprint(), nil, "二回目"); err != nil {
		t.Fatalf("受け手がペアリング済みなら確認なしで受け取る: %v", err)
	}
	if got := b.prompter.count(); got != 1 {
		t.Fatalf("2回目は問い合わせない: got %d", got)
	}
	if got := strings.Join(b.events.texts, "|"); got != "Mac:一回目|Mac:二回目" {
		t.Fatalf("受け取ったテキストが違う: %s", got)
	}

	a.prompter.decision = Decision{}
	connect(t, b, a)
	if err := b.Send(context.Background(), a.Fingerprint(), nil, "逆向き"); err == nil {
		t.Fatal("送り手側は未ペアリングのままなので、逆向きは承認が要る（断れば届かない）")
	}
	if a.prompter.count() != 1 {
		t.Fatal("逆向きの送信で送り手側に問い合わせが出ていない")
	}
}

func TestPairRequiresApprovalAndRegistersBoth(t *testing.T) {
	a, b := newTestNode(t, "Mac"), newTestNode(t, "Windows")
	connect(t, a, b)

	b.prompter.decision = Decision{}
	if err := a.Pair(context.Background(), b.Fingerprint(), func(string) {}, same); err == nil || !strings.Contains(err.Error(), "断りました") {
		t.Fatalf("断られたら理由つきで失敗する: %v", err)
	}
	if _, ok := a.store.Paired(b.Fingerprint()); ok {
		t.Fatal("断られたのに登録された")
	}

	b.prompter.decision = Decision{Accept: true}
	var code string
	if err := a.Pair(context.Background(), b.Fingerprint(), func(c string) { code = c }, same); err != nil {
		t.Fatal(err)
	}
	if code == "" || code != b.prompter.last().Code {
		t.Fatalf("申し込んだ側に、受けた側と同じ確認コードを渡す: %q / %q", code, b.prompter.last().Code)
	}
	if _, ok := a.store.Paired(b.Fingerprint()); !ok {
		t.Fatal("申し込んだ側に登録されていない")
	}
	pb, ok := b.store.Paired(a.Fingerprint())
	if !ok || pb.Name != "Mac" || pb.LastAddr != a.addr() {
		t.Fatalf("受けた側の登録内容が違う: %+v", pb)
	}
	if b.prompter.last().Kind != PromptPair {
		t.Fatalf("ペアリングの問い合わせの種類が違う: %s", b.prompter.last().Kind)
	}
}

func TestCodeDiffersPerConnection(t *testing.T) {
	a, b := newTestNode(t, "Mac"), newTestNode(t, "Windows")
	connect(t, a, b)
	b.prompter.decision = Decision{}
	_ = a.Send(context.Background(), b.Fingerprint(), nil, "1")
	_ = a.Send(context.Background(), b.Fingerprint(), nil, "2")
	if b.prompter.prompts[0].Code == b.prompter.prompts[1].Code {
		t.Fatal("確認コードは接続ごとに変わる（2台の指紋だけから作ると、割り込む側が前もって一致させられる）")
	}
}

func TestReplyAfterIdleTimeoutStillDelivered(t *testing.T) {
	// 承認や転送が無通信の期限より長くかかっても、最後の返事は書ける（期限は無通信の時間で数える）
	a, b := newTestNode(t, "Mac"), newTestNode(t, "Windows", withIdle(500*time.Millisecond))
	connect(t, a, b)

	b.prompter.decision, b.prompter.delay = Decision{Accept: true}, 1200*time.Millisecond
	if err := a.Send(context.Background(), b.Fingerprint(), nil, "遅い承認"); err != nil {
		t.Fatalf("承認が期限より遅くても送れる: %v", err)
	}
	if err := a.Pair(context.Background(), b.Fingerprint(), func(string) {}, same); err != nil {
		t.Fatalf("ペアリングの承認が期限より遅くても成立する: %v", err)
	}
	if _, ok := a.store.Paired(b.Fingerprint()); !ok {
		t.Fatal("申し込んだ側にも登録される")
	}

	// 転送そのものが期限より長くかかる場合（途中の無通信は期限より短い）
	conn, fp, err := a.dial(context.Background(), b.addr(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	body := bytes.Repeat([]byte("x"), 10*1024)
	if _, err := a.exchangeCode(conn, request{Kind: kindOffer, Files: []FileEntry{{Path: "slow.bin", Size: int64(len(body))}}}, fp); err != nil {
		t.Fatal(err)
	}
	var rep reply
	if err := a.awaitReply(conn, &rep, 5*time.Second); err != nil || !rep.Accepted {
		t.Fatalf("ペアリング済みなので承認なしで始まる: %v %+v", err, rep)
	}
	for i := 0; i < 10; i++ {
		time.Sleep(200 * time.Millisecond)
		if _, err := conn.Write(body[i*1024 : (i+1)*1024]); err != nil {
			t.Fatal(err)
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var fin reply
	if err := readFrame(conn, &fin); err != nil || !fin.Accepted {
		t.Fatalf("2 秒かかった転送でも保存完了の返事が届く: %v %+v", err, fin)
	}
}

func TestRejectedOfferLeavesNothing(t *testing.T) {
	a, b := newTestNode(t, "Mac"), newTestNode(t, "Windows")
	connect(t, a, b)
	src := filepath.Join(t.TempDir(), "x.txt")
	writeFile(t, src, []byte("x"))

	err := a.Send(context.Background(), b.Fingerprint(), []string{src}, "")
	if err == nil || !strings.Contains(err.Error(), "断りました") {
		t.Fatalf("断られたら理由つきで失敗する: %v", err)
	}
	if got := listDir(t, b.saveDir); len(got) != 0 {
		t.Fatalf("断ったのに保存先に何か残った: %v", got)
	}
	if p := a.events.last(Sending); p.State != StateFailed {
		t.Fatalf("送り手の進捗が失敗になっていない: %+v", p)
	}
}

func TestReceiverRejectsBadPathsBeforePrompt(t *testing.T) {
	// 送り手の検査をすり抜ける相手（改造した送り手）を、受け手が自分で断る
	a, b := newTestNode(t, "Mac"), newTestNode(t, "Windows")
	b.prompter.decision = Decision{Accept: true}
	for _, files := range [][]FileEntry{
		{{Path: "../evil.txt", Size: 1}},
		{{Path: "A", Dir: true}, {Path: "a/x.txt", Size: 1}},
		{{Path: "big1", Size: 1 << 62}, {Path: "big2", Size: 1 << 62}, {Path: "big3", Size: 1 << 62}},
	} {
		conn, fp, err := a.dial(context.Background(), b.addr(), "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.exchangeCode(conn, request{Kind: kindOffer, Files: files}, fp); err != nil {
			t.Fatal(err)
		}
		var rep reply
		if err := a.awaitReply(conn, &rep, 5*time.Second); err != nil {
			t.Fatal(err)
		}
		conn.Close()
		if rep.Accepted || !strings.Contains(rep.Reason, "受け取れない内容") {
			t.Fatalf("%v は断る: %+v", files, rep)
		}
	}
	if b.prompter.count() != 0 || len(listDir(t, b.saveDir)) != 0 {
		t.Fatal("断る内容で問い合わせを出したか、保存先に書いた")
	}
}

func TestRevealMismatchIsRejected(t *testing.T) {
	a, b := newTestNode(t, "Mac"), newTestNode(t, "Windows")
	b.prompter.decision = Decision{Accept: true}
	conn, _, err := a.dial(context.Background(), b.addr(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := a.sendRequest(conn, request{Kind: kindOffer, Text: "x", Commit: commitment(newNonce())}); err != nil {
		t.Fatal(err)
	}
	var ch challenge
	if err := readFrame(conn, &ch); err != nil {
		t.Fatal(err)
	}
	// 相手の乱数を見てから、先に送ったハッシュと違う乱数を明かす（コードを選ぼうとする割り込み）
	if err := writeFrame(conn, reveal{Nonce: newNonce()}); err != nil {
		t.Fatal(err)
	}
	var rep reply
	if err := a.awaitReply(conn, &rep, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if rep.Accepted || !strings.Contains(rep.Reason, "合いません") || b.prompter.count() != 0 {
		t.Fatalf("明かした乱数がハッシュと合わなければ、問い合わせる前に断る: %+v", rep)
	}
}

func TestNameCollisionGetsNumberedName(t *testing.T) {
	a, b := newTestNode(t, "Mac"), newTestNode(t, "Windows")
	connect(t, a, b)
	b.prompter.decision = Decision{Accept: true}
	writeFile(t, filepath.Join(b.saveDir, "a.txt"), []byte("old"))
	src := filepath.Join(t.TempDir(), "a.txt")
	writeFile(t, src, []byte("new"))

	if err := a.Send(context.Background(), b.Fingerprint(), []string{src}, ""); err != nil {
		t.Fatal(err)
	}
	old, _ := os.ReadFile(filepath.Join(b.saveDir, "a.txt"))
	nw, _ := os.ReadFile(filepath.Join(b.saveDir, "a (1).txt"))
	if string(old) != "old" || string(nw) != "new" {
		t.Fatalf("既存を上書きせず番号付きで保存する: old=%q new=%q", old, nw)
	}
}

func TestChangedFingerprintIsRefused(t *testing.T) {
	a, b := newTestNode(t, "Mac"), newTestNode(t, "Windows")
	fakeFP := strings.Repeat("ab", 32)
	if err := a.store.AddPeer(PairedPeer{Fingerprint: fakeFP, Name: "前のWindows", LastAddr: b.addr()}); err != nil {
		t.Fatal(err)
	}
	b.prompter.decision = Decision{Accept: true}

	err := a.Send(context.Background(), fakeFP, nil, "secret")
	if !strings.Contains(fmt.Sprint(err), "なりすまし") {
		t.Fatalf("ペアリング時と鍵が違う相手には送らない: %v", err)
	}
	if len(b.events.texts) != 0 || b.prompter.count() != 0 {
		t.Fatal("鍵が違う相手に内容が届いた")
	}
}

func TestSenderCancelWhileWaitingClosesReceiverPrompt(t *testing.T) {
	a, b := newTestNode(t, "Mac"), newTestNode(t, "Windows")
	connect(t, a, b)
	b.prompter.block = true
	b.prompter.closed = make(chan struct{})

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- a.Send(ctx, b.Fingerprint(), nil, "x") }()
	waitPrompt(t, b)
	cancel()
	if err := <-errc; err == nil || !strings.Contains(err.Error(), "取り消しました") {
		t.Fatalf("取り消したら取り消しとして失敗する: %v", err)
	}
	select {
	case <-b.prompter.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("送り手が取り消しても受け手の問い合わせが閉じない")
	}
	if len(b.events.texts) != 0 {
		t.Fatal("取り消した送信が届いた")
	}
}

func TestPairCanceledWhileAnsweringIsNotRegistered(t *testing.T) {
	// 申し込んだ側が取り消したのと同時に受けた側が承認しても、受けた側に登録しない
	a, b := newTestNode(t, "Mac"), newTestNode(t, "Windows")
	connect(t, a, b)
	b.prompter.block = true // ctx が切れたあとに「承認」を返す
	b.prompter.closed = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- a.Pair(ctx, b.Fingerprint(), func(string) {}, same) }()
	waitPrompt(t, b)
	cancel()
	if err := <-errc; err == nil || !strings.Contains(err.Error(), "取り消しました") {
		t.Fatalf("取り消したら取り消しとして失敗する: %v", err)
	}
	<-b.prompter.closed
	time.Sleep(100 * time.Millisecond)
	if _, ok := b.store.Paired(a.Fingerprint()); ok {
		t.Fatal("取り消されたペアリングが登録された")
	}
}

func TestOnePromptPerPeerAtATime(t *testing.T) {
	a, b := newTestNode(t, "Mac"), newTestNode(t, "Windows")
	connect(t, a, b)
	b.prompter.block = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = a.Send(ctx, b.Fingerprint(), nil, "1") }()
	waitPrompt(t, b)
	err := a.Send(context.Background(), b.Fingerprint(), nil, "2")
	if err == nil || !strings.Contains(err.Error(), "別の問い合わせ") {
		t.Fatalf("同じ相手からの問い合わせは同時に1件まで: %v", err)
	}
	if b.prompter.count() != 1 {
		t.Fatalf("2件目の問い合わせを出した: %d", b.prompter.count())
	}
}

func TestAllowRemoteRejectsConnection(t *testing.T) {
	a := newTestNode(t, "Mac")
	b := newTestNode(t, "Windows", func(o *Options) { o.AllowRemote = func(net.Addr) bool { return false } })
	if _, err := a.AddManual(context.Background(), b.addr()); err == nil {
		t.Fatal("受け入れない接続元からの接続は切る")
	}
}

func TestAddManualRejectsNonLAN(t *testing.T) {
	a := newTestNode(t, "Mac")
	_, err := a.AddManual(context.Background(), "8.8.8.8")
	if err == nil || !strings.Contains(err.Error(), "LAN の外") {
		t.Fatalf("LAN の外へは接続しない: %v", err)
	}
	_, err = a.AddManual(context.Background(), "あいう")
	if err == nil || !strings.Contains(err.Error(), "IP アドレスとして読めません") {
		t.Fatalf("読めない住所は理由つきで断る: %v", err)
	}
	_, err = a.AddManual(context.Background(), a.addr())
	if err == nil || !strings.Contains(err.Error(), "自身") {
		t.Fatalf("自分自身は追加しない: %v", err)
	}
}

func TestBeaconDoesNotOverwritePairedPeer(t *testing.T) {
	a, b := newTestNode(t, "Mac"), newTestNode(t, "Windows")
	if err := a.store.AddPeer(PairedPeer{Fingerprint: b.Fingerprint(), Name: "妻のWindows", LastAddr: b.addr()}); err != nil {
		t.Fatal(err)
	}
	a.seePeer(b.Fingerprint(), "偽物", "192.168.1.66:9", time.Now())
	pp, _ := a.store.Paired(b.Fingerprint())
	if pp.Name != "妻のWindows" || pp.LastAddr != b.addr() {
		t.Fatalf("認証されていない知らせで保存内容を変えない: %+v", pp)
	}
	for _, p := range a.Peers() {
		if p.Fingerprint == b.Fingerprint() && p.Name != "妻のWindows" {
			t.Fatalf("ペアリング済みの相手は保存した名前で出す: %q", p.Name)
		}
	}
}

func TestSeenPeersAreCapped(t *testing.T) {
	n := NewNode(Options{Events: &fakeEvents{}, Store: &Store{path: filepath.Join(t.TempDir(), "c.json")}})
	for i := 0; i < maxSeenPeers+10; i++ {
		n.seePeer(fmt.Sprintf("%064d", i), "x", "192.168.1.2:1", time.Now())
	}
	if len(n.seen) != maxSeenPeers {
		t.Fatalf("偽の知らせを大量に受けても一覧は %d 件まで: %d", maxSeenPeers, len(n.seen))
	}
}

func TestExpirePeersKeepsManual(t *testing.T) {
	n := NewNode(Options{Events: &fakeEvents{}, Store: &Store{path: filepath.Join(t.TempDir(), "c.json")}})
	now := time.Now()
	n.seePeer(strings.Repeat("b", 64), "auto", "192.168.1.2:1", now)
	n.seen[strings.Repeat("c", 64)] = &seenPeer{name: "manual", lastSeen: now, manual: true}
	n.expirePeers(now.Add(peerTTL + time.Second))
	if _, ok := n.seen[strings.Repeat("b", 64)]; ok {
		t.Fatal("知らせが途絶えた相手は一覧から消える")
	}
	if _, ok := n.seen[strings.Repeat("c", 64)]; !ok {
		t.Fatal("手入力の相手は残る")
	}
}

func TestPairNeedsInitiatorConfirmation(t *testing.T) {
	a, b := newTestNode(t, "Mac"), newTestNode(t, "Windows")
	connect(t, a, b)
	b.prompter.decision = Decision{Accept: true}
	notSame := func(context.Context) bool { return false }
	err := a.Pair(context.Background(), b.Fingerprint(), func(string) {}, notSame)
	if err == nil || !strings.Contains(err.Error(), "同じだと確かめられなかった") {
		t.Fatalf("申し込んだ側が「違う」と答えたら失敗する: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, ok := a.store.Paired(b.Fingerprint()); ok {
		t.Fatal("申し込んだ側に登録された")
	}
	if _, ok := b.store.Paired(a.Fingerprint()); ok {
		t.Fatal("申し込んだ側が確かめていないのに、受けた側に登録された")
	}
}

func TestPairNotSameClosesReceiverPrompt(t *testing.T) {
	// 受けた側が答える前に「違う」と答えたら、受けた側の問い合わせも閉じる
	a, b := newTestNode(t, "Mac"), newTestNode(t, "Windows")
	connect(t, a, b)
	b.prompter.block = true
	b.prompter.closed = make(chan struct{})
	answer := make(chan bool)
	errc := make(chan error, 1)
	go func() {
		errc <- a.Pair(context.Background(), b.Fingerprint(), func(string) {}, func(context.Context) bool { return <-answer })
	}()
	waitPrompt(t, b)
	answer <- false
	if err := <-errc; err == nil {
		t.Fatal("「違う」で失敗する")
	}
	select {
	case <-b.prompter.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("受けた側の問い合わせが閉じない")
	}
	time.Sleep(100 * time.Millisecond)
	if _, ok := b.store.Paired(a.Fingerprint()); ok {
		t.Fatal("受けた側に登録された")
	}
}

func TestChallengesFromUnpairedAreRateLimited(t *testing.T) {
	a, b := newTestNode(t, "Mac"), newTestNode(t, "Windows")
	connect(t, a, b)
	b.prompter.decision = Decision{} // 毎回すぐ断る
	for i := 0; i < maxChallengesPerMinute; i++ {
		if err := a.Send(context.Background(), b.Fingerprint(), nil, "x"); err == nil || !strings.Contains(err.Error(), "断りました") {
			t.Fatalf("%d 回目: %v", i, err)
		}
	}
	err := a.Send(context.Background(), b.Fingerprint(), nil, "x")
	if err == nil || !strings.Contains(err.Error(), "多すぎます") {
		t.Fatalf("未ペアリングの相手とのやりとりは 1 分に %d 回まで: %v", maxChallengesPerMinute, err)
	}
	if b.prompter.count() != maxChallengesPerMinute {
		t.Fatalf("上限を超えた分は問い合わせない: %d", b.prompter.count())
	}
	// ペアリング済みの相手は数えない
	if err := b.store.AddPeer(PairedPeer{Fingerprint: a.Fingerprint(), Name: "Mac"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Send(context.Background(), b.Fingerprint(), nil, "paired"); err != nil {
		t.Fatalf("ペアリング済みの相手は上限に関係なく送れる: %v", err)
	}
}

func TestConnectionsPerIPAreLimited(t *testing.T) {
	a, b := newTestNode(t, "Mac"), newTestNode(t, "Windows")
	var held []net.Conn
	for i := 0; i < maxConnsPerIP; i++ {
		c, _, err := a.dial(context.Background(), b.addr(), "")
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, c)
	}
	if _, err := a.AddManual(context.Background(), b.addr()); err == nil {
		t.Fatalf("同じ住所からの同時接続は %d 本まで", maxConnsPerIP)
	}
	for _, c := range held {
		c.Close()
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := a.AddManual(context.Background(), b.addr()); err != nil {
		t.Fatalf("接続を閉じれば、また繋がる: %v", err)
	}
}

func TestSeenPeersCapStillAdmitsPaired(t *testing.T) {
	st := &Store{path: filepath.Join(t.TempDir(), "c.json")}
	pairedFP := strings.Repeat("p", 64)
	if err := st.AddPeer(PairedPeer{Fingerprint: pairedFP, Name: "家族のMac"}); err != nil {
		t.Fatal(err)
	}
	n := NewNode(Options{Events: &fakeEvents{}, Store: st})
	for i := 0; i < maxSeenPeers; i++ {
		n.seePeer(fmt.Sprintf("%064d", i), "偽物", "192.168.1.2:1", time.Now())
	}
	n.seePeer(pairedFP, "家族のMac", "192.168.1.3:47821", time.Now())
	if _, ok := n.seen[pairedFP]; !ok {
		t.Fatal("一覧が偽の知らせで埋まっていても、ペアリング済みの相手は載せる")
	}
}
