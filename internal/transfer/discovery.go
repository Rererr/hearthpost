package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"
)

const (
	beaconApp      = "hearthpost"
	beaconInterval = 2 * time.Second
	peerTTL        = 7 * time.Second
)

type beacon struct {
	App  string `json:"app"`
	V    int    `json:"v"`
	FP   string `json:"fp"`
	Name string `json:"name"`
	Port int    `json:"port"`
}

// parseBeacon は受け取ったパケットを読む。他のアプリのパケット・自分のパケット・LAN 外からのパケットは捨てる。
func parseBeacon(b []byte, src netip.Addr, selfFP string) (beacon, bool) {
	var bc beacon
	if json.Unmarshal(b, &bc) != nil || bc.App != beaconApp || bc.V != protocolVersion {
		return beacon{}, false
	}
	if bc.FP == selfFP || len(bc.FP) != 64 || bc.Port <= 0 || bc.Port > 65535 || !IsLANIP(src) {
		return beacon{}, false
	}
	return bc, true
}

// broadcastTargets は LAN 側の各インターフェースの宛先ブロードキャスト住所と 255.255.255.255 を返す。
// 複数の NIC がある端末（有線と無線、仮想 NIC など）でも、それぞれの LAN へ届けるため。
func broadcastTargets(port int) []*net.UDPAddr {
	targets := []*net.UDPAddr{{IP: net.IPv4bcast, Port: port}}
	ifaces, err := net.Interfaces()
	if err != nil {
		return targets
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagBroadcast == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipn.IP.To4()
			if ip4 == nil || len(ipn.Mask) != net.IPv4len {
				continue
			}
			if addr, ok := netip.AddrFromSlice(ip4); !ok || !addr.IsPrivate() {
				continue
			}
			bc := make(net.IP, 4)
			for i := range ip4 {
				bc[i] = ip4[i] | ^ipn.Mask[i]
			}
			targets = append(targets, &net.UDPAddr{IP: bc, Port: port})
		}
	}
	return targets
}

// runDiscovery は自分の存在を定期的に知らせ、相手の知らせを受けて n の一覧を更新する。
func (n *Node) runDiscovery(ctx context.Context, pc net.PacketConn) {
	go func() {
		<-ctx.Done()
		pc.Close()
	}()
	go func() {
		t := time.NewTicker(beaconInterval)
		defer t.Stop()
		for {
			n.sendBeacon(pc)
			n.expirePeers(time.Now())
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	// Windows は受信バッファより大きいパケットをエラーとして返すので、UDP の最大長を確保する
	buf := make([]byte, 64<<10)
	for {
		sz, src, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			// パケット単位のエラー（大きすぎる・ICMP の到達不能など）では止めない。続けて返る場合に備えて少し待つ
			time.Sleep(50 * time.Millisecond)
			continue
		}
		udp, ok := src.(*net.UDPAddr)
		if !ok {
			continue
		}
		srcIP, ok := netip.AddrFromSlice(udp.IP)
		if !ok {
			continue
		}
		bc, ok := parseBeacon(buf[:sz], srcIP.Unmap(), n.id.Fingerprint)
		if !ok {
			continue
		}
		addr := netip.AddrPortFrom(srcIP.Unmap(), uint16(bc.Port)).String()
		n.seePeer(bc.FP, bc.Name, addr, time.Now())
	}
}

func (n *Node) sendBeacon(pc net.PacketConn) {
	b, err := json.Marshal(beacon{App: beaconApp, V: protocolVersion, FP: n.id.Fingerprint, Name: n.store.Name(), Port: n.tcpPort})
	if err != nil {
		return
	}
	// 届かない宛先（切断中の NIC など）があっても他の宛先には送り続け、全部だめなときだけ知らせる
	var lastErr error
	sent := false
	for _, t := range broadcastTargets(n.udpPort) {
		if _, err := pc.WriteTo(b, t); err != nil {
			lastErr = err
			continue
		}
		sent = true
	}
	if !sent && !n.beaconFailing.Swap(true) {
		n.events.Error(fmt.Sprintf("この端末の存在を LAN に知らせられません。相手の画面にこの端末が出ないので、相手の「IP アドレスで追加」にこの端末の IP を入れてもらってください（Mac ではローカルネットワークの許可も確かめてください）: %v", lastErr))
	}
	if sent {
		n.beaconFailing.Store(false)
	}
}
