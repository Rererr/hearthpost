# Go 1.27 以降では、Go 1.26 以前でビルドした wails CLI v2.13.0 が
# 「internal error: package "context" without types」で止まる（同梱の x/tools v0.44.0 が新しい標準ライブラリを読めない）。
# make wails-cli で x/tools を新しくした CLI を bin/wails に作り、それを使う。
WAILS ?= $(if $(wildcard bin/wails),./bin/wails,wails)

.PHONY: build build-mac build-windows dmg test wails-cli clean

build: build-mac build-windows

build-mac:
	$(WAILS) build -clean -platform darwin/universal

build-windows:
	$(WAILS) build -skipbindings -platform windows/amd64 -nsis -installscope user

# 配布用の dmg（背景に入れ方の案内。作り方は scripts/make-dmg.sh）
dmg: build-mac
	scripts/make-dmg.sh

# main.go が frontend/dist を埋め込むので、Go の検査より先に画面をビルドする
test:
	npm --prefix frontend ci
	npm --prefix frontend run build
	go vet ./...
	go test -race -count=1 ./...

wails-cli:
	rm -rf .wails-cli && mkdir -p .wails-cli bin
	cd .wails-cli && go mod init wailscli >/dev/null 2>&1 && \
		go get github.com/wailsapp/wails/v2/cmd/wails@v2.13.0 golang.org/x/tools@v0.52.0 && \
		go build -o ../bin/wails github.com/wailsapp/wails/v2/cmd/wails
	rm -rf .wails-cli

clean:
	rm -rf build/bin frontend/dist
