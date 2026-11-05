package transfer

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

var moveMu sync.Mutex

type sendItem struct {
	FileEntry
	abs string
}

// macMetadata は Mac が自動で作る隠しファイル。受け手に不要で、Icon\r は Windows で作れない名前なので送らない。
var macMetadata = map[string]bool{".DS_Store": true, "Icon\r": true}

// collectItems は送るパス（ファイルかフォルダ）を一覧にする。フォルダは中身の構成を保つ。
// シンボリックリンクはたどらず、数だけ返す（リンク先が送り手の意図しない場所を指すことがあるため）。
func collectItems(paths []string) (items []sendItem, skippedLinks int, err error) {
	for _, p := range paths {
		info, err := os.Lstat(p)
		if err != nil {
			return nil, 0, fmt.Errorf("%s を開けません: %w", p, err)
		}
		base := filepath.Base(p)
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			skippedLinks++
		case info.Mode().IsRegular():
			items = append(items, sendItem{FileEntry{Path: base, Size: info.Size()}, p})
		case info.IsDir():
			err := filepath.WalkDir(p, func(cur string, d fs.DirEntry, err error) error {
				if err != nil {
					return fmt.Errorf("%s を読めません: %w", cur, err)
				}
				rel, err := filepath.Rel(p, cur)
				if err != nil {
					return err
				}
				entryPath := path.Join(base, filepath.ToSlash(rel))
				switch {
				case macMetadata[d.Name()]:
				case d.Type()&fs.ModeSymlink != 0:
					skippedLinks++
				case d.IsDir():
					items = append(items, sendItem{FileEntry{Path: entryPath, Dir: true}, cur})
				case d.Type().IsRegular():
					fi, err := d.Info()
					if err != nil {
						return fmt.Errorf("%s を読めません: %w", cur, err)
					}
					items = append(items, sendItem{FileEntry{Path: entryPath, Size: fi.Size()}, cur})
				}
				// 名前付きパイプなどの特殊ファイルは中身が定まらないので送らない
				return nil
			})
			if err != nil {
				return nil, 0, err
			}
		default:
			return nil, 0, fmt.Errorf("%s は通常のファイルでもフォルダでもないため送れません", p)
		}
	}
	if len(items) == 0 {
		if skippedLinks > 0 {
			return nil, skippedLinks, fmt.Errorf("送れるものがありません（シンボリックリンク %d 件は送りません）", skippedLinks)
		}
		return nil, 0, errors.New("送れるものがありません")
	}
	return items, skippedLinks, nil
}

func sendFileBody(w io.Writer, it sendItem) error {
	f, err := os.Open(it.abs)
	if err != nil {
		return fmt.Errorf("%s を開けません: %w", it.abs, err)
	}
	defer f.Close()
	n, err := io.CopyN(w, f, it.Size)
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("%s が送信中に短くなりました（%d / %d バイト）。書き換え中のファイルは送り直してください", it.abs, n, it.Size)
	}
	if err != nil {
		return fmt.Errorf("%s を送れません: %w", it.abs, err)
	}
	return nil
}

// receiveFiles は files を保存先の作業フォルダへ受け取り、全部そろってから保存先へ移す。
// 途中で失敗したら作業フォルダごと消すので、保存先に半端なファイルが残らない。
func receiveFiles(r io.Reader, saveDir string, files []FileEntry, onBytes func(int)) (saved []string, err error) {
	staging, err := os.MkdirTemp(saveDir, ".hearthpost-receiving-")
	if errors.Is(err, fs.ErrPermission) {
		return nil, fmt.Errorf("保存先 %s に書き込む許可がありません（Mac では「システム設定 → プライバシーとセキュリティ → ファイルとフォルダ」で hearthpost を許可するか、保存先を変えてください）: %w", saveDir, err)
	}
	if err != nil {
		return nil, fmt.Errorf("保存先 %s に書き込めません: %w", saveDir, err)
	}
	defer os.RemoveAll(staging)

	var tops []string
	topIsDir := map[string]bool{}
	for _, f := range files {
		top, _, nested := strings.Cut(f.Path, "/")
		if _, ok := topIsDir[top]; !ok {
			tops = append(tops, top)
		}
		topIsDir[top] = topIsDir[top] || nested || f.Dir
		dst := filepath.Join(staging, filepath.FromSlash(f.Path))
		if f.Dir {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return nil, fmt.Errorf("フォルダ %s を作れません: %w", f.Path, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, fmt.Errorf("フォルダ %s を作れません: %w", path.Dir(f.Path), err)
		}
		if err := receiveOne(r, dst, f, onBytes); err != nil {
			return nil, err
		}
	}

	// 名前の空きを確かめてから移すまでの間に別の受信が同じ名前を取ると、Unix の rename は黙って上書きする
	moveMu.Lock()
	defer moveMu.Unlock()
	for _, top := range tops {
		final, err := uniquePath(saveDir, top, topIsDir[top])
		if err != nil {
			return saved, err
		}
		if err := os.Rename(filepath.Join(staging, top), final); err != nil {
			return saved, fmt.Errorf("%s を保存先へ移せません: %w", top, err)
		}
		saved = append(saved, final)
	}
	return saved, nil
}

func receiveOne(r io.Reader, dst string, f FileEntry, onBytes func(int)) error {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("%s を作れません: %w", f.Path, err)
	}
	_, err = io.CopyN(&countingWriter{out, onBytes}, r, f.Size)
	closeErr := out.Close()
	if err != nil {
		return fmt.Errorf("%s の受信が途中で切れました: %w", f.Path, err)
	}
	if closeErr != nil {
		return fmt.Errorf("%s を書き込めません: %w", f.Path, closeErr)
	}
	return nil
}

// uniquePath は保存先に同じ名前があれば「名前 (1).拡張子」のように番号を付けた住所を返す。
// フォルダと「.env」のようにドットで始まる名前は、拡張子に分けずに末尾へ番号を付ける。
func uniquePath(dir, name string, isDir bool) (string, error) {
	ext := filepath.Ext(name)
	if isDir || ext == name {
		ext = ""
	}
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 10000; i++ {
		cand := name
		if i > 0 {
			cand = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		p := filepath.Join(dir, cand)
		_, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			return p, nil
		}
		if err != nil {
			return "", fmt.Errorf("保存先 %s を確かめられません: %w", p, err)
		}
	}
	return "", fmt.Errorf("保存先に %s と同じ名前が多すぎます", name)
}

type countingWriter struct {
	w  io.Writer
	on func(int)
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if n > 0 && c.on != nil {
		c.on(n)
	}
	return n, err
}
