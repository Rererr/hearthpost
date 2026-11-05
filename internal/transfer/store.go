package transfer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// PairedPeer はペアリング済みの相手。ここに載っている相手からの送信は確認なしで受け取る。
type PairedPeer struct {
	Fingerprint string    `json:"fingerprint"`
	Name        string    `json:"name"`
	LastAddr    string    `json:"lastAddr,omitempty"`
	PairedAt    time.Time `json:"pairedAt"`
}

type settings struct {
	Name    string       `json:"name"`
	SaveDir string       `json:"saveDir"`
	Peers   []PairedPeer `json:"peers"`
}

// Store は設定とペアリング済みの相手を JSON で保存する。
type Store struct {
	path string
	mu   sync.Mutex
	s    settings
}

// OpenStore は path の設定を読む。無ければ既定値で作る。
func OpenStore(path, defaultName, defaultSaveDir string) (*Store, error) {
	st := &Store{path: path, s: settings{Name: defaultName, SaveDir: defaultSaveDir}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, fmt.Errorf("設定ファイル %s を読めません: %w", path, err)
	}
	if err := json.Unmarshal(b, &st.s); err != nil {
		return nil, fmt.Errorf("設定ファイル %s が壊れています（削除すると既定値で作り直します。ペアリングはやり直しになります）: %w", path, err)
	}
	if st.s.Name == "" {
		st.s.Name = defaultName
	}
	if st.s.SaveDir == "" {
		st.s.SaveDir = defaultSaveDir
	}
	return st, nil
}

func (st *Store) Name() string {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s.Name
}

func (st *Store) SaveDir() string {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s.SaveDir
}

func (st *Store) SetName(name string) error {
	if name == "" {
		return errors.New("端末名を空にはできません")
	}
	return st.update(func(s *settings) { s.Name = name })
}

func (st *Store) SetSaveDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("保存先 %s を開けません: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("保存先 %s はフォルダではありません", dir)
	}
	return st.update(func(s *settings) { s.SaveDir = dir })
}

// Paired は fp がペアリング済みなら、その相手を返す。
func (st *Store) Paired(fp string) (PairedPeer, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	i := slices.IndexFunc(st.s.Peers, func(p PairedPeer) bool { return p.Fingerprint == fp })
	if i < 0 {
		return PairedPeer{}, false
	}
	return st.s.Peers[i], true
}

func (st *Store) PairedPeers() []PairedPeer {
	st.mu.Lock()
	defer st.mu.Unlock()
	return slices.Clone(st.s.Peers)
}

// AddPeer はペアリング済みの相手を登録する。登録済みなら名前と住所を更新する。
func (st *Store) AddPeer(p PairedPeer) error {
	return st.update(func(s *settings) {
		i := slices.IndexFunc(s.Peers, func(q PairedPeer) bool { return q.Fingerprint == p.Fingerprint })
		if i < 0 {
			s.Peers = append(s.Peers, p)
			return
		}
		s.Peers[i].Name = p.Name
		if p.LastAddr != "" {
			s.Peers[i].LastAddr = p.LastAddr
		}
	})
}

// Touch はペアリング済みの相手の名前と最後に見えた住所を更新する。未登録なら何もしない。
func (st *Store) Touch(fp, name, addr string) error {
	st.mu.Lock()
	i := slices.IndexFunc(st.s.Peers, func(q PairedPeer) bool { return q.Fingerprint == fp })
	unchanged := i < 0 || (st.s.Peers[i].Name == name && st.s.Peers[i].LastAddr == addr)
	st.mu.Unlock()
	if unchanged {
		return nil
	}
	return st.update(func(s *settings) {
		for j := range s.Peers {
			if s.Peers[j].Fingerprint == fp {
				s.Peers[j].Name = name
				s.Peers[j].LastAddr = addr
			}
		}
	})
}

func (st *Store) RemovePeer(fp string) error {
	return st.update(func(s *settings) {
		s.Peers = slices.DeleteFunc(s.Peers, func(p PairedPeer) bool { return p.Fingerprint == fp })
	})
}

func (st *Store) update(f func(*settings)) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	next := st.s
	next.Peers = slices.Clone(st.s.Peers)
	f(&next)
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("設定を書き出せません: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(st.path), 0o700); err != nil {
		return fmt.Errorf("設定フォルダを作れません: %w", err)
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("設定を保存できません: %w", err)
	}
	if err := os.Rename(tmp, st.path); err != nil {
		return fmt.Errorf("設定を保存できません: %w", err)
	}
	st.s = next
	return nil
}
