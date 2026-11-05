package transfer

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"
)

// idleConn は読み書きのたびに期限を延ばす。転送全体ではなく「無通信の時間」で切るため。
type idleConn struct {
	net.Conn
	idle time.Duration
}

func (c idleConn) Read(p []byte) (int, error) {
	_ = c.Conn.SetReadDeadline(time.Now().Add(c.idle))
	return c.Conn.Read(p)
}

func (c idleConn) Write(p []byte) (int, error) {
	_ = c.Conn.SetWriteDeadline(time.Now().Add(c.idle))
	return c.Conn.Write(p)
}

func (n *Node) handle(ctx context.Context, raw net.Conn) {
	defer raw.Close()
	tconn := tls.Server(raw, &tls.Config{
		Certificates: []tls.Certificate{n.id.Cert},
		MinVersion:   tls.VersionTLS13,
		// 自己署名の証明書を受け入れ、指紋で相手を見分ける。鍵を持っていることは TLS の署名検証が確かめる
		ClientAuth: tls.RequireAnyClientCert,
	})
	hctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	if err := tconn.HandshakeContext(hctx); err != nil {
		return // LAN 内の別のソフトがポートを叩いただけのことも多いので、画面には出さない
	}
	peerFP := fingerprint(tconn.ConnectionState().PeerCertificates[0].Raw)
	conn := idleConn{tconn, n.idleTimeout}

	// 最初の要求は期限を延ばさずに読む。少しずつ送り続けて接続の枠を握り続けられないように
	_ = tconn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	var req request
	if err := readFrame(tconn, &req); err != nil {
		return
	}
	if req.Version != protocolVersion {
		_ = writeFrame(conn, reply{Reason: fmt.Sprintf("相手の hearthpost と通信の版が違います（相手 %d・この端末 %d）。両方を同じ版にそろえてください", req.Version, protocolVersion)})
		return
	}
	peerName := cleanName(req.Name)
	peerAddr := ""
	if ap, err := netip.ParseAddrPort(raw.RemoteAddr().String()); err == nil && req.Port > 0 && req.Port <= 65535 {
		peerAddr = netip.AddrPortFrom(ap.Addr().Unmap(), uint16(req.Port)).String()
	}
	n.touch(peerFP, peerName, peerAddr)

	if req.Kind == kindHello {
		_ = writeFrame(conn, reply{Accepted: true, Name: n.store.Name()})
		return
	}
	if req.Kind != kindPair && req.Kind != kindOffer {
		_ = writeFrame(conn, reply{Reason: fmt.Sprintf("知らない要求 %q です", req.Kind)})
		return
	}
	code, err := n.answerCode(conn, req, peerFP)
	if err != nil {
		_ = writeFrame(conn, reply{Reason: err.Error()})
		return
	}
	if req.Kind == kindPair {
		n.handlePair(ctx, conn, tconn, peerFP, peerName, peerAddr, code)
		return
	}
	n.handleOffer(ctx, conn, tconn, peerFP, peerName, peerAddr, code, req)
}

// answerCode は受けた側として確認コード用の乱数をやりとりする。
// 相手が先に送ったハッシュ（req.Commit）と、あとで明かした乱数が合わなければ断る。
func (n *Node) answerCode(conn io.ReadWriter, req request, peerFP string) (string, error) {
	if len(req.Commit) != 32 {
		return "", errors.New("確認コード用の値がありません（hearthpost の版が違う可能性があります）")
	}
	if _, paired := n.store.Paired(peerFP); !paired && !n.allowChallenge(time.Now()) {
		return "", errors.New("確認コードのやりとりが短い間に多すぎます。1 分ほど待ってから送り直してください")
	}
	nonce := newNonce()
	if err := writeFrame(conn, challenge{Nonce: nonce}); err != nil {
		return "", err
	}
	var rv reveal
	if err := readFrame(conn, &rv); err != nil {
		return "", err
	}
	if len(rv.Nonce) != 32 || !bytes.Equal(commitment(rv.Nonce), req.Commit) {
		return "", errors.New("確認コード用の値が最初に送られたものと合いません")
	}
	return sessionCode(peerFP, n.id.Fingerprint, rv.Nonce, nonce), nil
}

var errBusy = errors.New("相手が別の問い合わせに応答している最中です。少し待ってから送り直してください")

// ask は受け手の人に問い合わせる。待っている間に送り手が取り消して接続が切れたら、問い合わせも閉じる。
// 同じ相手からの問い合わせは同時に1件、全体で maxPendingPrompts 件までに抑える。
func (n *Node) ask(ctx context.Context, tconn net.Conn, p Prompt) (Decision, error) {
	n.mu.Lock()
	if n.prompting[p.PeerFP] || len(n.prompting) >= maxPendingPrompts {
		n.mu.Unlock()
		return Decision{}, errBusy
	}
	n.prompting[p.PeerFP] = true
	n.mu.Unlock()
	defer func() {
		n.mu.Lock()
		delete(n.prompting, p.PeerFP)
		n.mu.Unlock()
	}()

	p.ID = newID()
	deadline := time.Now().Add(n.promptTimeout)
	p.ExpiresAt = deadline.UnixMilli()
	pctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	// 送り手は返事を待つ間なにも送らないので、読めた時点（切断か規約違反）で取り消しとみなす
	_ = tconn.SetReadDeadline(time.Time{})
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		var b [1]byte
		if _, err := tconn.Read(b[:]); !errors.Is(err, os.ErrDeadlineExceeded) {
			cancel()
		}
	}()
	d := n.prompter.Ask(pctx, p)
	_ = tconn.SetReadDeadline(time.Now())
	<-watched
	if pctx.Err() != nil {
		return Decision{}, nil
	}
	return d, nil
}

func (n *Node) remember(fp, name, addr string) error {
	return n.store.AddPeer(PairedPeer{Fingerprint: fp, Name: name, LastAddr: addr, PairedAt: time.Now()})
}

func (n *Node) rejectReason() string {
	return fmt.Sprintf("相手が断りました（または %d 秒以内に応答がありませんでした）", int(n.promptTimeout/time.Second))
}

func (n *Node) handlePair(ctx context.Context, conn io.Writer, tconn net.Conn, peerFP, peerName, peerAddr, code string) {
	if _, ok := n.store.Paired(peerFP); !ok {
		d, err := n.ask(ctx, tconn, Prompt{Kind: PromptPair, PeerName: peerName, PeerFP: peerFP, Code: code})
		if err != nil {
			_ = writeFrame(conn, reply{Reason: err.Error()})
			return
		}
		if !d.Accept {
			_ = writeFrame(conn, reply{Reason: "ペアリングを" + n.rejectReason()})
			return
		}
	}
	if err := writeFrame(conn, reply{Accepted: true, Name: n.store.Name()}); err != nil {
		return
	}
	// 申し込んだ側の人がコードを見比べ終えるまで待つ
	_ = tconn.SetReadDeadline(time.Now().Add(n.promptTimeout))
	var pc pairConfirm
	if err := readFrame(tconn, &pc); err != nil || !pc.OK {
		return
	}
	if err := n.remember(peerFP, peerName, peerAddr); err != nil {
		n.events.Error(err.Error())
		_ = writeFrame(conn, reply{Reason: "相手側でペアリングを保存できませんでした"})
		return
	}
	n.events.PeersChanged()
	_ = writeFrame(conn, reply{Accepted: true})
}

func (n *Node) handleOffer(ctx context.Context, conn io.ReadWriter, tconn net.Conn, peerFP, peerName, peerAddr, code string, req request) {
	total, err := checkOffer(req.Files, req.Text)
	if err != nil {
		_ = writeFrame(conn, reply{Reason: "受け取れない内容です: " + err.Error()})
		return
	}

	if _, ok := n.store.Paired(peerFP); !ok {
		fileCount := 0
		for _, f := range req.Files {
			if !f.Dir {
				fileCount++
			}
		}
		d, err := n.ask(ctx, tconn, Prompt{Kind: PromptOffer, PeerName: peerName, PeerFP: peerFP, Code: code, FileCount: fileCount, TotalSize: total, TopNames: topNames(req.Files), Text: req.Text})
		if err != nil {
			_ = writeFrame(conn, reply{Reason: err.Error()})
			return
		}
		if !d.Accept {
			_ = writeFrame(conn, reply{Reason: "受け取りを" + n.rejectReason()})
			return
		}
		if d.Pair {
			if err := n.remember(peerFP, peerName, peerAddr); err != nil {
				n.events.Error(err.Error())
			} else {
				n.events.PeersChanged()
			}
		}
	}

	id, tctx, done := n.track(ctx)
	defer done()
	stop := context.AfterFunc(tctx, func() { tconn.Close() })
	defer stop()

	p := Progress{ID: id, Direction: Receiving, PeerName: peerName, Label: offerLabel(req.Files), Total: total, State: StateRunning}
	if err := writeFrame(conn, reply{Accepted: true, Name: n.store.Name()}); err != nil {
		return
	}
	n.events.Progress(p)

	if len(req.Files) > 0 {
		var th throttle
		saved, err := receiveFiles(conn, n.store.SaveDir(), req.Files, func(k int) {
			p.Done += int64(k)
			if th.ready(time.Now()) {
				n.events.Progress(p)
			}
		})
		p.Saved = saved
		if err != nil {
			p.State, p.Error = StateFailed, err.Error()
			switch {
			case tctx.Err() != nil:
				p.State, p.Error = StateCanceled, "取り消しました"
			case isConnClosed(err):
				p.Error = "送り手との通信が切れました（送り手が取り消した可能性があります）"
			}
			if len(saved) > 0 {
				p.Error = fmt.Sprintf("%d 件だけ保存したところで止まりました: %s", len(saved), p.Error)
			}
			n.events.Progress(p)
			_ = writeFrame(conn, reply{Reason: "受け手で保存できませんでした: " + p.Error})
			return
		}
	}
	if req.Text != "" {
		n.events.TextReceived(peerName, req.Text)
	}
	p.State = StateDone
	n.events.Progress(p)
	_ = writeFrame(conn, reply{Accepted: true})
}

func topNames(files []FileEntry) []string {
	var tops []string
	seen := map[string]bool{}
	for _, f := range files {
		top, _, _ := strings.Cut(f.Path, "/")
		if !seen[top] {
			seen[top] = true
			tops = append(tops, top)
		}
	}
	return tops
}

func offerLabel(files []FileEntry) string {
	tops := topNames(files)
	switch len(tops) {
	case 0:
		return "テキスト"
	case 1:
		return tops[0]
	default:
		return fmt.Sprintf("%s ほか %d 件", tops[0], len(tops)-1)
	}
}

func isConnClosed(err error) bool {
	var ne net.Error
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) || errors.As(err, &ne)
}
