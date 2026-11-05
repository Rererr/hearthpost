package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"hearthpost/internal/transfer"
)

// App は画面（フロントエンド）から呼ぶ操作をまとめる。
type App struct {
	ctx      context.Context
	node     *transfer.Node
	store    *transfer.Store
	startErr string

	mu         sync.Mutex
	pending    map[string]pendingPrompt
	pairCancel context.CancelFunc
	pairAnswer chan bool
}

func NewApp() *App {
	return &App{pending: map[string]pendingPrompt{}}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if err := a.start(ctx); err != nil {
		a.startErr = err.Error()
	}
}

func (a *App) start(ctx context.Context) error {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("設定フォルダの場所が分かりません: %w", err)
	}
	cfgDir = filepath.Join(cfgDir, "hearthpost")
	id, err := transfer.LoadOrCreateIdentity(cfgDir)
	if err != nil {
		return err
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "hearthpost"
	}
	host = strings.TrimSuffix(host, ".local")
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("ホームフォルダの場所が分かりません: %w", err)
	}
	st, err := transfer.OpenStore(filepath.Join(cfgDir, "config.json"), host, filepath.Join(home, "Downloads"))
	if err != nil {
		return err
	}
	a.store = st
	a.node = transfer.NewNode(transfer.Options{
		Identity: id, Store: st,
		TCPPort: transfer.DefaultTCPPort, UDPPort: transfer.DefaultUDPPort,
		Prompter: bridge{a}, Events: bridge{a},
	})
	return a.node.Start(ctx)
}

// bridge は transfer.Prompter / transfer.Events を実装し、画面へイベントで伝える。
// App に直接実装すると Wails がこれらのメソッドも画面から呼べる形で公開してしまうため分ける。
type bridge struct{ *App }

type pendingPrompt struct {
	prompt transfer.Prompt
	answer chan transfer.Decision
}

func (a bridge) Ask(ctx context.Context, p transfer.Prompt) transfer.Decision {
	ch := make(chan transfer.Decision, 1)
	a.mu.Lock()
	a.pending[p.ID] = pendingPrompt{p, ch}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.pending, p.ID)
		a.mu.Unlock()
	}()
	runtime.EventsEmit(a.ctx, "prompt", p)
	runtime.WindowShow(a.ctx) // 最小化していても承認を求めていることに気づけるように
	select {
	case d := <-ch:
		return d
	case <-ctx.Done():
		runtime.EventsEmit(a.ctx, "prompt-closed", p.ID)
		return transfer.Decision{}
	}
}

func (a bridge) PeersChanged()                { runtime.EventsEmit(a.ctx, "peers", a.node.Peers()) }
func (a bridge) Progress(p transfer.Progress) { runtime.EventsEmit(a.ctx, "progress", p) }
func (a bridge) Error(msg string)             { runtime.EventsEmit(a.ctx, "notice", msg) }
func (a bridge) TextReceived(fromName, text string) {
	runtime.EventsEmit(a.ctx, "text", map[string]string{"from": fromName, "text": text})
}

// --- 画面から呼ぶ操作 ---

type State struct {
	Name        string          `json:"name"`
	Fingerprint string          `json:"fingerprint"`
	SaveDir     string          `json:"saveDir"`
	Port        int             `json:"port"`
	Addresses   []string        `json:"addresses"`
	StartError  string          `json:"startError"`
	Peers       []transfer.Peer `json:"peers"`
	OS          string          `json:"os"`
	// Prompts は答えを待っている問い合わせ。画面がイベントを受け始める前に届いたものを取りこぼさないため
	Prompts []transfer.Prompt `json:"prompts"`
}

func (a *App) GetState() State {
	s := State{StartError: a.startErr, Addresses: lanAddresses(), OS: goruntime.GOOS, Peers: []transfer.Peer{}, Prompts: []transfer.Prompt{}}
	a.mu.Lock()
	for _, pp := range a.pending {
		s.Prompts = append(s.Prompts, pp.prompt)
	}
	a.mu.Unlock()
	slices.SortFunc(s.Prompts, func(x, y transfer.Prompt) int { return cmp.Compare(x.ExpiresAt, y.ExpiresAt) })
	if a.store != nil {
		s.Name, s.SaveDir = a.store.Name(), a.store.SaveDir()
	}
	if a.node != nil && a.startErr == "" {
		s.Fingerprint = a.node.Fingerprint()
		s.Port = a.node.Port()
		s.Peers = a.node.Peers()
	}
	return s
}

var errNotStarted = errors.New("起動に失敗しているため使えません（画面上部の理由を確かめてください）")

func (a *App) ready() error {
	if a.node == nil || a.startErr != "" {
		return errNotStarted
	}
	return nil
}

func (a *App) SetName(name string) error {
	if a.store == nil {
		return errNotStarted
	}
	return a.store.SetName(strings.TrimSpace(name))
}

func (a *App) ChooseSaveDir() (string, error) {
	if a.store == nil {
		return "", errNotStarted
	}
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "受け取ったファイルの保存先", DefaultDirectory: a.store.SaveDir()})
	if err != nil {
		return "", fmt.Errorf("フォルダを選べません: %w", err)
	}
	if dir == "" {
		return a.store.SaveDir(), nil // 選ぶのをやめた
	}
	if err := a.store.SetSaveDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func (a *App) PickFiles() ([]string, error) {
	paths, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{Title: "送るファイル"})
	if err != nil {
		return nil, fmt.Errorf("ファイルを選べません: %w", err)
	}
	return paths, nil
}

func (a *App) PickFolder() (string, error) {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "送るフォルダ"})
	if err != nil {
		return "", fmt.Errorf("フォルダを選べません: %w", err)
	}
	return dir, nil
}

func (a *App) SendPaths(fp string, paths []string) error {
	if err := a.ready(); err != nil {
		return err
	}
	if len(paths) == 0 {
		return errors.New("送るものが選ばれていません")
	}
	go a.send(fp, paths, "")
	return nil
}

// send の失敗は transfer.Node が進捗イベント（state=failed と理由）で画面へ伝えるので、ここでは返さない。
// 呼び出しを待たせると画面が相手の承認（最大 60 秒）まで止まるため、送信は裏で進める。
func (a *App) send(fp string, paths []string, text string) {
	_ = a.node.Send(a.ctx, fp, paths, text)
}

func (a *App) SendText(fp, text string) error {
	if err := a.ready(); err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("テキストが空です")
	}
	go a.send(fp, nil, text)
	return nil
}

func (a *App) ReadClipboard() (string, error) {
	s, err := runtime.ClipboardGetText(a.ctx)
	if err != nil {
		return "", fmt.Errorf("クリップボードを読めません: %w", err)
	}
	return s, nil
}

func (a *App) CopyText(text string) error {
	if err := runtime.ClipboardSetText(a.ctx, text); err != nil {
		return fmt.Errorf("クリップボードにコピーできません: %w", err)
	}
	return nil
}

// Pair は相手の承認を待つ間ブロックする。CancelPair で取り消せる。
func (a *App) Pair(fp string) error {
	if err := a.ready(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(a.ctx)
	defer cancel()
	a.mu.Lock()
	if a.pairCancel != nil {
		a.mu.Unlock()
		return errors.New("別のペアリングを申し込み中です。終わってからやり直してください")
	}
	a.pairCancel = cancel
	answer := make(chan bool, 1)
	a.pairAnswer = answer
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.pairCancel, a.pairAnswer = nil, nil
		a.mu.Unlock()
	}()
	onCode := func(code string) { runtime.EventsEmit(a.ctx, "pair-code", code) }
	confirm := func(ctx context.Context) bool {
		select {
		case ok := <-answer:
			return ok
		case <-ctx.Done():
			return false
		}
	}
	return a.node.Pair(ctx, fp, onCode, confirm)
}

// ConfirmPairCode は、ペアリングを申し込んだ側の人が確認コードを見比べた結果を伝える。
func (a *App) ConfirmPairCode(same bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pairAnswer == nil {
		return errors.New("申し込み中のペアリングがありません")
	}
	select {
	case a.pairAnswer <- same:
	default: // 2回押した。最初の答えを使う
	}
	return nil
}

func (a *App) CancelPair() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pairCancel != nil {
		a.pairCancel()
	}
}

func (a *App) Unpair(fp string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.node.Unpair(fp)
}

func (a *App) AddManual(addr string) (transfer.Peer, error) {
	if err := a.ready(); err != nil {
		return transfer.Peer{}, err
	}
	return a.node.AddManual(a.ctx, addr)
}

func (a *App) Respond(id string, accept, pair bool) error {
	a.mu.Lock()
	pp, ok := a.pending[id]
	a.mu.Unlock()
	if !ok {
		return errors.New("この問い合わせはもう閉じています（送り手が取り消したか、応答の期限が過ぎました）")
	}
	select {
	case pp.answer <- transfer.Decision{Accept: accept, Pair: pair}:
	default: // 同じ問い合わせに2回答えた。最初の答えを使う
	}
	return nil
}

func (a *App) Cancel(id string) bool {
	if a.node == nil {
		return false
	}
	return a.node.Cancel(id)
}

// ShowInFolder は受け取ったファイルをファイルマネージャで選んだ状態で開く。
func (a *App) ShowInFolder(path string) error { return openInFileManager(path, true) }

func (a *App) OpenSaveDir() error {
	if a.store == nil {
		return errNotStarted
	}
	return openInFileManager(a.store.SaveDir(), false)
}

func openInFileManager(path string, reveal bool) error {
	var cmd *exec.Cmd
	switch {
	case goruntime.GOOS == "darwin" && reveal:
		cmd = exec.Command("open", "-R", path)
	case goruntime.GOOS == "darwin":
		cmd = exec.Command("open", path)
	case goruntime.GOOS == "windows" && reveal:
		cmd = exec.Command("explorer", "/select,", path)
	case goruntime.GOOS == "windows":
		cmd = exec.Command("explorer", path)
	default:
		cmd = exec.Command("xdg-open", filepath.Dir(path))
	}
	// explorer は成功しても終了コード 1 を返すので、起動できたかだけを見る
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s を開けません: %w", path, err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// lanAddresses は IP の手入力用に、この端末の LAN 側 IPv4 アドレスを返す。
func lanAddresses() []string {
	out := []string{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, ad := range addrs {
			ipn, ok := ad.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil {
				continue
			}
			ip, ok := netip.AddrFromSlice(ipn.IP.To4())
			if ok && ip.IsPrivate() {
				out = append(out, ip.String())
			}
		}
	}
	return out
}
