# hearthpost

[English](README.en.md)

hearthpost は、同じ LAN にいる Windows と Mac のあいだで、ファイル、フォルダ、テキストを送り合うデスクトップアプリです。
インターネットやクラウドを通さず、端末どうしが直接つながって送ります。

- 同じ LAN で hearthpost を起動している端末を自動で見つけます。見つからないときは IP アドレスを入力して追加できます。
- 送り先のカードにファイルやフォルダをドラッグ＆ドロップすると送ります。フォルダは中の構成を保ったまま届きます。
- テキストを送るときは、クリップボードの内容が最初から入っています。
- 通信は TLS 1.3 で暗号化します。LAN の外のアドレスとは通信しません。
- 未ペアリングの相手から届いたときは、そのたびに承認を求めます。ペアリング済みの相手からは確認なしで受け取ります。
- 承認するときは、2 台の画面に出る 6 桁の確認コードを見比べて、別の端末が割り込んでいないことを確かめられます。コードは接続ごとに変わります。
- ペアリングでは両方の画面でコードを見比べ、以後は端末の鍵で相手を確かめます。未ペアリングの相手へ送るときは、送る側は相手の承認だけで送り始めるので、大事なものを送る相手とは先にペアリングしてください。

仕組みと設計の理由は [docs/DESIGN.md](docs/DESIGN.md) に書いています。

## 使い方

1. 2 台の端末で hearthpost を起動します。しばらくすると、互いの名前が「送り先」に並びます。
2. 送り先のカードの「ファイル」「フォルダ」「テキスト」を押すか、カードの上にファイルをドロップします。
3. 相手が未ペアリングなら、相手の画面に承認のダイアログが出ます。確認コードが自分の画面と同じなら、相手に「受け取る」を押してもらいます。
4. 受け取ったファイルは、画面の上部に表示している保存先（既定はダウンロードフォルダ）に入ります。保存先に同じ名前があれば「名前 (1).拡張子」として保存し、上書きしません。

毎回の承認を省きたい相手とは、カードの「ペアリング」から申し込みます。
両方の画面に出る確認コードが同じなら、自分の画面で「同じ数字です」を押し、相手にも承認してもらいます。
すると、2 台とも互いから届いたものを確認なしで受け取るようになります。
承認のダイアログで「受け取ってペアリング」を選んだときは、受け取った側だけが相手を登録します（送った側の設定は変わりません）。
解除はその端末の側だけに効くので、両方で解除したいときは相手の画面でも解除してください。

### 相手が見つからないとき

- 2 台とも同じ Wi-Fi、または同じルーターにつないでください。
- Windows では、ネットワークの種類を「プライベート」にしてください。「パブリック」では受信が止められることがあります。
- ゲスト用 Wi-Fi、ルーターの「AP 分離（プライバシーセパレーター）」、VPN は、端末どうしの通信を止めます。
- それでも見つからなければ、相手の画面の上部に出ている IP アドレスを「IP アドレスで追加」に入れてください。

## インストール

いまは配布用のビルドを用意していないので、ソースからビルドします。

### 必要なもの

- Go 1.25 以降
- Node.js 20 以降と npm
- Wails CLI v2.13.0（下の手順で入れます）
- Mac：Xcode Command Line Tools（`xcode-select --install`）
- Windows：WebView2 ランタイム（Windows 10 と 11 には通常入っています）

### Mac

```sh
git clone https://github.com/Rererr/hearthpost.git
cd hearthpost
make wails-cli      # bin/wails に Wails CLI を作る（下の注意を参照）
make build-mac      # build/bin/hearthpost.app ができる
cp -R build/bin/hearthpost.app /Applications/
```

ファイアウォールを有効にしている Mac では、初めて相手から接続されたときに「“hearthpost”へのネットワーク受信接続を許可しますか？」と聞かれます。
「許可」を押してください。
「拒否」を押すと受け取れなくなります。あとから変えるには「システム設定 → ネットワーク → ファイアウォール → オプション」で hearthpost を許可してください。

macOS 15 以降でローカルネットワークの許可を求められたら、許可してください。
許可は「システム設定 → プライバシーとセキュリティ → ローカルネットワーク」で変えられます。

### Windows

PowerShell で 1 行ずつ実行します。
Wails CLI は、x/tools を新しくしたものを `bin\wails.exe` に作って使います（理由は下の注意を参照）。

```powershell
git clone https://github.com/Rererr/hearthpost.git
cd hearthpost
mkdir .wails-cli
cd .wails-cli
go mod init wailscli
go get github.com/wailsapp/wails/v2/cmd/wails@v2.13.0 golang.org/x/tools@v0.52.0
go build -o ..\bin\wails.exe github.com/wailsapp/wails/v2/cmd/wails
cd ..
Remove-Item -Recurse -Force .wails-cli
.\bin\wails.exe build -platform windows/amd64
```

`build\bin\hearthpost.exe` ができます。好きな場所に置いて起動してください。
Windows Defender ファイアウォールの許可を求める画面が出たら、「プライベート ネットワーク」を許可してください。

Mac から Windows 版を作ることもできます（`make build-windows`）。

### Wails CLI についての注意

Go 1.27 では、Wails CLI v2.13.0 をそのまま `go install` すると、`wails build` が `internal error: package "context" without types was imported` で止まります。
CLI が使う x/tools v0.44.0 が、Go 1.27 の標準ライブラリを読めないためです（x/tools v0.52.0 では読めることを確かめています）。
上の手順と `make wails-cli` は、x/tools を v0.52.0 に上げた CLI を作ります。

### アンインストール

1. アプリを消します。Mac は `/Applications/hearthpost.app`、Windows は置いた `hearthpost.exe` です。
2. 設定フォルダを消します。端末の鍵とペアリングの記録が入っています。
   - Mac：`~/Library/Application Support/hearthpost`
   - Windows：`%AppData%\hearthpost`

受け取ったファイルは保存先に残ります。

## LLM 向けのインストール用プロンプト

コーディングエージェント（Claude Code など）に、次をそのまま貼ってください。

```text
hearthpost（https://github.com/Rererr/hearthpost）をソースからビルドして、この端末で使える状態にしてください。

前提の確認:
- OS が macOS か Windows かを確かめる。
- go version で Go 1.25 以降、node --version で Node.js 20 以降があるか確かめる。無ければ、インストール方法を案内して止まる（勝手に入れない）。
- macOS では xcode-select -p で Command Line Tools があるか確かめる。

手順:
1. 作業用のフォルダに git clone https://github.com/Rererr/hearthpost.git して、その中に入る。
2. macOS: make wails-cli を実行してから make build-mac を実行する。build/bin/hearthpost.app ができたことを確かめる。
   Windows（PowerShell）: README の「Windows」節のコマンドを上から順に実行する。build\bin\hearthpost.exe ができたことを確かめる。PowerShell 5.1 では && が使えないので、コマンドは1つずつ実行する。
3. wails build が「internal error: package "context" without types was imported」で止まったら、x/tools を新しくした CLI（bin/wails）を使っているか確かめる。
4. go test ./... を実行して、テストが通ることを確かめる。
5. macOS では、/Applications へコピーしてよいかを人に聞き、許可されたときだけ cp -R build/bin/hearthpost.app /Applications/ を実行する。

人に案内すること（エージェントにはできない操作）:
- 初めて相手から接続されたとき、macOS のファイアウォールが受信の許可を求めたら「許可」を押す。Windows Defender ファイアウォールの画面が出たら「プライベート ネットワーク」を許可する。
- macOS 15 以降でローカルネットワークの許可を求められたら許可する。
- もう1台の端末でも同じようにインストールして起動し、互いが「送り先」に並ぶことを確かめる。

守ること:
- sudo や管理者権限を使わない。
- clone したリポジトリと /Applications（許可を得た場合のみ）以外を変更しない。Go と npm の通常のキャッシュは除く。
- ファイアウォールやネットワークの設定を変えない。
```

## 開発

```sh
make test         # 画面のビルド、go vet、テスト（-race）
make build        # Mac（universal）と Windows の両方をビルド
```

通信の中心は `internal/transfer` にあり、Wails に依存しません。
画面は `frontend/src` の React です。

## ライセンス

MIT License。[LICENSE](LICENSE) を参照してください。
