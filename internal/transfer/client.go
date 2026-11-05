package transfer

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Pair は相手にペアリングを申し込み、相手が承認するまで待つ。
// onCode には、相手の画面と見比べる確認コードを承認待ちに入る前に渡す。
// confirm は、この端末の人が相手の画面と同じコードだと確かめたときだけ true を返す。
// 相手の承認だけで登録すると、同じ名前を名乗る別の端末が承認するだけでペアリングできてしまうため、両側で見比べる。
func (n *Node) Pair(ctx context.Context, fp string, onCode func(code string), confirm func(ctx context.Context) bool) error {
	addr, err := n.peerAddr(fp)
	if err != nil {
		return err
	}
	conn, gotFP, err := n.dialPeer(ctx, addr, fp)
	if err != nil {
		n.forget(fp)
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	canceled := func(err error) error {
		if ctx.Err() != nil {
			return errors.New("ペアリングを取り消しました")
		}
		return err
	}
	code, err := n.exchangeCode(conn, request{Kind: kindPair}, gotFP)
	if err != nil {
		return canceled(err)
	}
	onCode(code)
	confirmed := make(chan bool, 1)
	cctx, stopConfirm := context.WithCancel(ctx)
	defer stopConfirm()
	go func() { confirmed <- confirm(cctx) }()
	var rep reply
	replied := make(chan error, 1)
	go func() { replied <- n.awaitReply(conn, &rep, n.promptTimeout+handshakeTimeout) }()

	// 相手の承認と、この端末の人の確認を、どちらが先でも待つ。「違う」が先なら切って終える（相手の問い合わせも閉じる）
	notSame := errors.New("確認コードが同じだと確かめられなかったため、ペアリングしませんでした（どちらの端末にも登録していません）")
	var ok, gotOK, gotReply bool
	for !gotOK || !gotReply {
		select {
		case ok = <-confirmed:
			gotOK = true
			if !ok {
				if ctx.Err() != nil {
					return errors.New("ペアリングを取り消しました")
				}
				return notSame
			}
		case err := <-replied:
			gotReply = true
			if err != nil {
				return canceled(err)
			}
			if !rep.Accepted {
				return errors.New(rep.Reason)
			}
		}
	}
	if err := writeFrame(idleConn{conn, n.idleTimeout}, pairConfirm{OK: true}); err != nil {
		return fmt.Errorf("相手に送れません: %w", err)
	}
	var fin reply
	if err := n.awaitReply(conn, &fin, n.idleTimeout); err != nil {
		return fmt.Errorf("相手がペアリングを保存したか確かめられません: %w", err)
	}
	if !fin.Accepted {
		return errors.New(fin.Reason)
	}
	if err := n.remember(gotFP, cleanName(rep.Name), addr); err != nil {
		return err
	}
	n.events.PeersChanged()
	return nil
}

// Unpair はこの端末の側だけペアリングを解除する。相手の側の登録は相手の画面で解除してもらう。
func (n *Node) Unpair(fp string) error {
	if err := n.store.RemovePeer(fp); err != nil {
		return err
	}
	n.events.PeersChanged()
	return nil
}

// Send はファイル・フォルダ（paths）とテキストを相手に送り、相手が保存し終えるまで待つ。
// 進捗は Events.Progress で通知し、その ID を Cancel に渡すと取り消せる。
func (n *Node) Send(ctx context.Context, fp string, paths []string, text string) error {
	id, ctx, done := n.track(ctx)
	defer done()

	peerName := fp[:min(8, len(fp))]
	for _, p := range n.Peers() {
		if p.Fingerprint == fp {
			peerName = p.Name
		}
	}
	p := Progress{ID: id, Direction: Sending, PeerName: peerName, State: StateWaiting}
	fail := func(err error) error {
		p.State, p.Error = StateFailed, err.Error()
		if ctx.Err() != nil {
			p.State, p.Error = StateCanceled, "取り消しました"
		}
		n.events.Progress(p)
		return errors.New(p.Error)
	}

	var items []sendItem
	if len(paths) > 0 {
		var skipped int
		var err error
		items, skipped, err = collectItems(paths)
		if err != nil {
			return fail(err)
		}
		if skipped > 0 {
			n.events.Error(fmt.Sprintf("シンボリックリンク %d 件は送りません（リンク先が意図しない場所を指すことがあるため）", skipped))
		}
	}
	req := request{Kind: kindOffer, Text: text}
	for _, it := range items {
		req.Files = append(req.Files, it.FileEntry)
	}
	total, err := checkOffer(req.Files, req.Text)
	if err != nil {
		return fail(err)
	}
	p.Label, p.Total = offerLabel(req.Files), total
	n.events.Progress(p)

	addr, err := n.peerAddr(fp)
	if err != nil {
		return fail(err)
	}
	conn, gotFP, err := n.dialPeer(ctx, addr, fp)
	if err != nil {
		if ctx.Err() == nil {
			n.forget(fp)
		}
		return fail(err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	code, err := n.exchangeCode(conn, req, gotFP)
	if err != nil {
		return fail(err)
	}
	p.Code = code
	n.events.Progress(p)
	var rep reply
	if err := n.awaitReply(conn, &rep, n.promptTimeout+handshakeTimeout); err != nil {
		return fail(err)
	}
	if !rep.Accepted {
		return fail(errors.New(rep.Reason))
	}
	n.touch(gotFP, cleanName(rep.Name), addr)

	p.State = StateRunning
	n.events.Progress(p)
	var th throttle
	cw := &countingWriter{w: idleConn{conn, n.idleTimeout}, on: func(k int) {
		p.Done += int64(k)
		if th.ready(time.Now()) {
			n.events.Progress(p)
		}
	}}
	for _, it := range items {
		if it.Dir {
			continue
		}
		if err := sendFileBody(cw, it); err != nil {
			return fail(err)
		}
	}
	var fin reply
	_ = conn.SetReadDeadline(time.Now().Add(n.idleTimeout))
	if err := readFrame(conn, &fin); err != nil {
		return fail(fmt.Errorf("相手が保存し終えたか確かめられません: %w", err))
	}
	if !fin.Accepted {
		return fail(errors.New(fin.Reason))
	}
	p.State = StateDone
	n.events.Progress(p)
	return nil
}
