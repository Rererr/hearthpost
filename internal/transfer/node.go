package transfer

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	DefaultTCPPort = 47821
	DefaultUDPPort = 47820

	handshakeTimeout = 10 * time.Second
	// DefaultPromptTimeout は受け手が承認ダイアログに答えるまでの待ち時間。
	DefaultPromptTimeout = 60 * time.Second
	defaultIdleTimeout   = 30 * time.Second

	maxConns      = 32 // 同時に扱う接続。超えた分はすぐ切る
	maxConnsPerIP = 4  // 同じ住所からの同時接続。1台に枠を埋めさせないため
	// maxChallengesPerMinute は未ペアリングの相手との確認コードのやりとりの上限（全体）。
	// 外れたら乱数を明かさずに切って繰り返すと、割り込む側が両側のコードを一致させられるため
	maxChallengesPerMinute = 30
	maxPendingPrompts      = 3  // 同時に出す承認の問い合わせ。未ペアリングの相手に画面を埋めさせないため
	maxSeenPeers           = 64 // 端末探しで覚える相手の数。偽の知らせを大量に送られても一覧が膨らまないように
)

type PromptKind string

const (
	PromptPair  PromptKind = "pair"
	PromptOffer PromptKind = "offer"
)

// Prompt は受け手の画面に出す承認の問い合わせ。
type Prompt struct {
	ID        string     `json:"id"`
	Kind      PromptKind `json:"kind"`
	PeerName  string     `json:"peerName"`
	PeerFP    string     `json:"peerFingerprint"`
	Code      string     `json:"code"`
	FileCount int        `json:"fileCount"`
	TotalSize int64      `json:"totalSize"`
	TopNames  []string   `json:"topNames"`
	Text      string     `json:"text"`
	// ExpiresAt はこの時刻（Unix ミリ秒）を過ぎると断ったものとして扱う。画面の残り秒数もここから数える。
	ExpiresAt int64 `json:"expiresAt"`
}

type Decision struct {
	Accept bool
	// Pair は受信の承認と同時にペアリングするか（Kind が offer のときだけ意味を持つ）。
	Pair bool
}

// Prompter は受け手の人に承認を求める。ctx が切れたら拒否として返す。
type Prompter interface {
	Ask(ctx context.Context, p Prompt) Decision
}

type Direction string

const (
	Sending   Direction = "send"
	Receiving Direction = "receive"
)

type TransferState string

const (
	StateWaiting  TransferState = "waiting" // 接続中、または受け手の承認待ち
	StateRunning  TransferState = "running"
	StateDone     TransferState = "done"
	StateFailed   TransferState = "failed"
	StateCanceled TransferState = "canceled"
)

type Progress struct {
	ID        string        `json:"id"`
	Direction Direction     `json:"direction"`
	PeerName  string        `json:"peerName"`
	Label     string        `json:"label"`
	Done      int64         `json:"done"`
	Total     int64         `json:"total"`
	State     TransferState `json:"state"`
	Error     string        `json:"error,omitempty"`
	Saved     []string      `json:"saved,omitempty"`
	Code      string        `json:"code,omitempty"`
}

type Events interface {
	PeersChanged()
	Progress(Progress)
	TextReceived(fromName, text string)
	Error(msg string)
}

// Peer は画面の一覧に出す相手。Paired は「この端末が確認なしで受け取る相手か」を表す。
// 相手の側がこの端末をペアリング済みとして扱うかは、この端末からは分からない。
type Peer struct {
	Fingerprint string `json:"fingerprint"`
	Name        string `json:"name"`
	Addr        string `json:"addr"`
	Paired      bool   `json:"paired"`
	Online      bool   `json:"online"`
}

type seenPeer struct {
	name     string
	addr     string
	lastSeen time.Time
	manual   bool // IP の手入力で追加した相手は、知らせが届かないので期限切れにしない
}

type Options struct {
	Identity *Identity
	Store    *Store
	TCPPort  int
	UDPPort  int // 0 なら端末探しを動かさない（テスト用）
	Prompter Prompter
	Events   Events
	// 0 なら既定値。テストで期限の境界を短時間で跨ぐために変える
	PromptTimeout time.Duration
	IdleTimeout   time.Duration
	// AllowRemote は受け入れる接続元を決める。nil なら IsLANAddr
	AllowRemote func(net.Addr) bool
}

type Node struct {
	id            *Identity
	store         *Store
	tcpPort       int
	udpPort       int
	prompter      Prompter
	events        Events
	promptTimeout time.Duration
	idleTimeout   time.Duration
	allowRemote   func(net.Addr) bool
	conns         chan struct{}

	mu         sync.Mutex
	perIP      map[netip.Addr]int
	challenges []time.Time
	seen       map[string]*seenPeer
	transfers  map[string]context.CancelFunc
	prompting  map[string]bool // 問い合わせ中の相手の指紋

	beaconFailing atomic.Bool
}

func NewNode(o Options) *Node {
	n := &Node{
		id: o.Identity, store: o.Store, tcpPort: o.TCPPort, udpPort: o.UDPPort,
		prompter: o.Prompter, events: o.Events,
		promptTimeout: o.PromptTimeout, idleTimeout: o.IdleTimeout, allowRemote: o.AllowRemote,
		conns: make(chan struct{}, maxConns),
		perIP: map[netip.Addr]int{},
		seen:  map[string]*seenPeer{}, transfers: map[string]context.CancelFunc{}, prompting: map[string]bool{},
	}
	if n.promptTimeout == 0 {
		n.promptTimeout = DefaultPromptTimeout
	}
	if n.idleTimeout == 0 {
		n.idleTimeout = defaultIdleTimeout
	}
	if n.allowRemote == nil {
		n.allowRemote = IsLANAddr
	}
	return n
}

func (n *Node) Fingerprint() string { return n.id.Fingerprint }

// Start は受信の待ち受けと端末探しを始める。ctx が切れると止まる。
func (n *Node) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", n.tcpPort))
	if err != nil {
		return fmt.Errorf("受信用のポート %d を開けません（hearthpost がもう起動していないか確かめてください）: %w", n.tcpPort, err)
	}
	if n.tcpPort == 0 {
		n.tcpPort = ln.Addr().(*net.TCPAddr).Port
	}
	if n.udpPort != 0 {
		pc, err := net.ListenPacket("udp4", fmt.Sprintf(":%d", n.udpPort))
		if err != nil {
			ln.Close()
			return fmt.Errorf("端末探し用のポート %d を開けません（hearthpost がもう起動していないか確かめてください）: %w", n.udpPort, err)
		}
		go n.runDiscovery(ctx, pc)
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	go n.acceptLoop(ctx, ln)
	return nil
}

func (n *Node) Port() int { return n.tcpPort }

func (n *Node) acceptLoop(ctx context.Context, ln net.Listener) {
	var backoff time.Duration
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			// ファイル記述子の枯渇などは一時的なので、待ってから受け付け直す（net/http と同じ）
			backoff = min(max(backoff*2, 5*time.Millisecond), time.Second)
			time.Sleep(backoff)
			continue
		}
		backoff = 0
		if !n.allowRemote(conn.RemoteAddr()) {
			conn.Close()
			continue
		}
		ip := remoteIP(conn.RemoteAddr())
		if !n.takeSlot(ip) {
			conn.Close()
			continue
		}
		select {
		case n.conns <- struct{}{}:
			go func() {
				defer func() { <-n.conns; n.releaseSlot(ip) }()
				n.handle(ctx, conn)
			}()
		default:
			n.releaseSlot(ip)
			conn.Close()
		}
	}
}

func remoteIP(a net.Addr) netip.Addr {
	ap, err := netip.ParseAddrPort(a.String())
	if err != nil {
		return netip.Addr{}
	}
	return ap.Addr().Unmap()
}

func (n *Node) takeSlot(ip netip.Addr) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.perIP[ip] >= maxConnsPerIP {
		return false
	}
	n.perIP[ip]++
	return true
}

func (n *Node) releaseSlot(ip netip.Addr) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.perIP[ip]--; n.perIP[ip] <= 0 {
		delete(n.perIP, ip)
	}
}

func (n *Node) allowChallenge(now time.Time) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.challenges = slices.DeleteFunc(n.challenges, func(t time.Time) bool { return now.Sub(t) > time.Minute })
	if len(n.challenges) >= maxChallengesPerMinute {
		return false
	}
	n.challenges = append(n.challenges, now)
	return true
}

// --- 相手の一覧 ---

// seePeer は端末探しの知らせで一覧を更新する。知らせは認証されていないので、設定ファイルには書かない。
func (n *Node) seePeer(fp, name, addr string, now time.Time) {
	name = cleanName(name)
	_, paired := n.store.Paired(fp)
	n.mu.Lock()
	p, ok := n.seen[fp]
	// 一覧が埋まっていても、ペアリング済みの相手は載せる（偽の知らせで埋められても見失わないように）
	if !ok && !paired && len(n.seen) >= maxSeenPeers {
		n.mu.Unlock()
		return
	}
	changed := !ok || p.name != name || p.addr != addr
	if !ok {
		p = &seenPeer{}
		n.seen[fp] = p
	}
	p.name, p.addr, p.lastSeen = name, addr, now
	n.mu.Unlock()
	if changed {
		n.events.PeersChanged()
	}
}

func (n *Node) expirePeers(now time.Time) {
	n.mu.Lock()
	changed := false
	for fp, p := range n.seen {
		if !p.manual && now.Sub(p.lastSeen) > peerTTL {
			delete(n.seen, fp)
			changed = true
		}
	}
	n.mu.Unlock()
	if changed {
		n.events.PeersChanged()
	}
}

// forget は接続できなかった相手を「見つかっている」一覧から外す。端末探しの知らせが届けばまた載る。
func (n *Node) forget(fp string) {
	n.mu.Lock()
	_, ok := n.seen[fp]
	delete(n.seen, fp)
	n.mu.Unlock()
	if ok {
		n.events.PeersChanged()
	}
}

// Peers は見つかっている相手とペアリング済みの相手を合わせて返す。
// ペアリング済みの相手の名前は、認証した接続で受け取った名前（設定ファイルの値）を使う。
func (n *Node) Peers() []Peer {
	n.mu.Lock()
	byFP := map[string]*Peer{}
	for fp, s := range n.seen {
		byFP[fp] = &Peer{Fingerprint: fp, Name: s.name, Addr: s.addr, Online: true}
	}
	n.mu.Unlock()
	for _, pp := range n.store.PairedPeers() {
		if p, ok := byFP[pp.Fingerprint]; ok {
			p.Paired, p.Name = true, pp.Name
			continue
		}
		byFP[pp.Fingerprint] = &Peer{Fingerprint: pp.Fingerprint, Name: pp.Name, Addr: pp.LastAddr, Paired: true}
	}
	out := make([]Peer, 0, len(byFP))
	for _, p := range byFP {
		out = append(out, *p)
	}
	slices.SortFunc(out, func(a, b Peer) int {
		if a.Online != b.Online {
			if a.Online {
				return -1
			}
			return 1
		}
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.Fingerprint, b.Fingerprint)
	})
	return out
}

func (n *Node) peerAddr(fp string) (string, error) {
	n.mu.Lock()
	s, ok := n.seen[fp]
	n.mu.Unlock()
	if ok {
		return s.addr, nil
	}
	if pp, ok := n.store.Paired(fp); ok && pp.LastAddr != "" {
		return pp.LastAddr, nil
	}
	return "", errors.New("相手が LAN 内に見つかりません（相手のアプリが起動しているか、同じネットワークにいるか確かめてください）")
}

// AddManual は IP の手入力で相手を追加する。端末探しの知らせがルーターなどに遮られる環境向け。
func (n *Node) AddManual(ctx context.Context, addr string) (Peer, error) {
	addr = strings.TrimSpace(addr)
	if _, err := netip.ParseAddrPort(addr); err != nil {
		ip, perr := netip.ParseAddr(addr)
		if perr != nil {
			return Peer{}, fmt.Errorf("%q は IP アドレスとして読めません（例: 192.168.1.20）", addr)
		}
		addr = netip.AddrPortFrom(ip, DefaultTCPPort).String()
	}
	conn, fp, err := n.dial(ctx, addr, "")
	if err != nil {
		return Peer{}, err
	}
	defer conn.Close()
	if fp == n.id.Fingerprint {
		return Peer{}, errors.New("それはこの端末自身の住所です")
	}
	var rep reply
	if err := n.roundTrip(conn, request{Kind: kindHello}, &rep, handshakeTimeout); err != nil {
		return Peer{}, err
	}
	name := cleanName(rep.Name)
	n.mu.Lock()
	n.seen[fp] = &seenPeer{name: name, addr: addr, lastSeen: time.Now(), manual: true}
	n.mu.Unlock()
	n.touch(fp, name, addr)
	n.events.PeersChanged()
	_, paired := n.store.Paired(fp)
	return Peer{Fingerprint: fp, Name: name, Addr: addr, Paired: paired, Online: true}, nil
}

// touch は認証した接続で分かった、ペアリング済みの相手の名前と住所を保存する。
func (n *Node) touch(fp, name, addr string) {
	if err := n.store.Touch(fp, name, addr); err != nil {
		n.events.Error(err.Error())
	}
}

// --- 通信の土台 ---

var errFingerprintChanged = errors.New("相手の鍵が前回と違います。別の端末がなりすましている可能性があるため中止しました（相手がアプリを入れ直した場合は、ペアリングを解除してからやり直してください）")

var errFingerprintMismatch = errors.New("一覧に出ていた端末と違う端末が応答しました。別の端末が同じ住所を名乗っている可能性があるため中止しました。しばらく待ってから送り直してください")

// dialPeer は一覧の相手 fp に接続する。鍵の食い違いは、ペアリング済みかどうかで理由を分けて返す。
func (n *Node) dialPeer(ctx context.Context, addr, fp string) (*tls.Conn, string, error) {
	conn, got, err := n.dial(ctx, addr, fp)
	if errors.Is(err, errFingerprintChanged) {
		if _, paired := n.store.Paired(fp); !paired {
			return nil, "", errFingerprintMismatch
		}
	}
	return conn, got, err
}

func (n *Node) dial(ctx context.Context, addr, wantFP string) (*tls.Conn, string, error) {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return nil, "", fmt.Errorf("住所 %q を読めません: %w", addr, err)
	}
	if !IsLANIP(ap.Addr()) {
		return nil, "", fmt.Errorf("%s は LAN の外の住所なので接続しません", addr)
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, "", fmt.Errorf("%s に接続できません（相手のアプリが起動しているか、ファイアウォールで止められていないか確かめてください）: %w", addr, err)
	}
	var gotFP string
	conn := tls.Client(raw, &tls.Config{
		Certificates:       []tls.Certificate{n.id.Cert},
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, // 自己署名なので認証局では確かめず、下の指紋で確かめる
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("相手が証明書を出しませんでした")
			}
			gotFP = fingerprint(rawCerts[0])
			if wantFP != "" && gotFP != wantFP {
				return errFingerprintChanged
			}
			return nil
		},
	})
	hctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	if err := conn.HandshakeContext(hctx); err != nil {
		raw.Close()
		if errors.Is(err, errFingerprintChanged) {
			return nil, "", errFingerprintChanged
		}
		return nil, "", fmt.Errorf("%s との暗号化の準備に失敗しました: %w", addr, err)
	}
	return conn, gotFP, nil
}

func (n *Node) roundTrip(conn net.Conn, req request, rep *reply, wait time.Duration) error {
	if err := n.sendRequest(conn, req); err != nil {
		return err
	}
	return n.awaitReply(conn, rep, wait)
}

func (n *Node) sendRequest(conn net.Conn, req request) error {
	req.Version = protocolVersion
	req.Name = n.store.Name()
	req.Port = n.tcpPort
	_ = conn.SetDeadline(time.Now().Add(n.idleTimeout))
	if err := writeFrame(conn, req); err != nil {
		return fmt.Errorf("相手に送れません: %w", err)
	}
	return nil
}

func (n *Node) awaitReply(conn net.Conn, rep *reply, wait time.Duration) error {
	_ = conn.SetReadDeadline(time.Now().Add(wait))
	if err := readFrame(conn, rep); err != nil {
		return fmt.Errorf("相手から返事がありません: %w", err)
	}
	return nil
}

// exchangeCode は接続した側として確認コード用の乱数をやりとりし、確認コードを返す。
// 先に乱数のハッシュ（req.Commit）を送っておき、相手の乱数を受け取ってから元の乱数を明かす。
func (n *Node) exchangeCode(conn net.Conn, req request, peerFP string) (string, error) {
	nonce := newNonce()
	req.Commit = commitment(nonce)
	if err := n.sendRequest(conn, req); err != nil {
		return "", err
	}
	var ch challenge
	if err := readFrame(conn, &ch); err != nil {
		return "", fmt.Errorf("相手から返事がありません: %w", err)
	}
	if ch.Reason != "" {
		return "", errors.New(ch.Reason)
	}
	if len(ch.Nonce) != 32 {
		return "", errors.New("相手が確認コード用の値を正しく返しませんでした（hearthpost の版が違う可能性があります）")
	}
	if err := writeFrame(conn, reveal{Nonce: nonce}); err != nil {
		return "", fmt.Errorf("相手に送れません: %w", err)
	}
	return sessionCode(n.id.Fingerprint, peerFP, nonce, ch.Nonce), nil
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // crypto/rand.Read は Go 1.24 以降エラーを返さない
	return hex.EncodeToString(b)
}

func (n *Node) track(ctx context.Context) (string, context.Context, func()) {
	id := newID()
	ctx, cancel := context.WithCancel(ctx)
	n.mu.Lock()
	n.transfers[id] = cancel
	n.mu.Unlock()
	return id, ctx, func() {
		cancel()
		n.mu.Lock()
		delete(n.transfers, id)
		n.mu.Unlock()
	}
}

// Cancel は送受信中の id を取り消す。もう終わっていれば false。
func (n *Node) Cancel(id string) bool {
	n.mu.Lock()
	cancel, ok := n.transfers[id]
	n.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// throttle は進捗の通知を間引く。毎回送ると大きなファイルで画面への通知が数十万回になるため。
type throttle struct {
	last time.Time
}

func (t *throttle) ready(now time.Time) bool {
	if now.Sub(t.last) < 100*time.Millisecond {
		return false
	}
	t.last = now
	return true
}
