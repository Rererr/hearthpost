# hearthpost

[日本語](README.md)

hearthpost is a desktop app for sending files, folders, and text between Windows and Mac computers on the same LAN.
Devices connect to each other directly. Nothing goes through the internet or a cloud service.

- Finds other devices running hearthpost on the same LAN automatically. If one is not found, you can add it by IP address.
- Drag and drop files or folders onto a device card to send them. Folders arrive with their structure intact.
- When you send text, the text box starts with your clipboard contents.
- Traffic is encrypted with TLS 1.3. hearthpost never talks to addresses outside the LAN.
- Transfers from unpaired devices need your approval each time. Transfers from paired devices are accepted without asking.
- When approving, compare the 6-digit confirmation code shown on both screens to make sure no other device is in the middle. The code changes on every connection.
- Pairing compares the code on both screens, and after that each device checks the other by its key. When you send to an unpaired device, sending starts as soon as the other side approves, so pair first with devices you send important things to.

How it works and why it is built this way: [docs/DESIGN.md](docs/DESIGN.md) (Japanese).

## Usage

1. Start hearthpost on two devices. After a few seconds, each appears under "送り先" (destinations) on the other.
2. Press "ファイル" (files), "フォルダ" (folder), or "テキスト" (text) on a device card, or drop files onto the card.
3. If the receiver is not paired, an approval dialog appears on the receiver. If the confirmation code matches yours, ask them to press "受け取る" (accept).
4. Received files go to the save folder shown at the top of the window (Downloads by default). If a file with the same name exists, the new one is saved as "name (1).ext". Nothing is overwritten.

To skip approval for a device, request pairing with "ペアリング" (pair) on its card.
If the confirmation code is the same on both screens, press "同じ数字です" (same number) on your screen and have the other side approve.
Then both devices accept each other's transfers without asking.
Choosing "受け取ってペアリング" (accept and pair) in the approval dialog registers the sender only on the receiving device (the sender's settings do not change).
Unpairing affects only the device where you do it; to unpair both ways, unpair on the other device too.

The UI is in Japanese only for now.

### If a device is not found

- Connect both devices to the same Wi-Fi network or router.
- On Windows, set the network profile to "Private". On "Public" networks, incoming connections may be blocked.
- Guest Wi-Fi, router "AP isolation" (client isolation), and VPNs block traffic between devices.
- If it is still not found, type the IP address shown at the top of the other device's window into "IP アドレスで追加" (add by IP address).

## Installation

There are no prebuilt releases yet. Build from source.

### Requirements

- Go 1.25 or later
- Node.js 20 or later, with npm
- Wails CLI v2.13.0 (the steps below build it)
- Mac: Xcode Command Line Tools (`xcode-select --install`)
- Windows: WebView2 Runtime (usually preinstalled on Windows 10 and 11)

### Mac

```sh
git clone https://github.com/Rererr/hearthpost.git
cd hearthpost
make wails-cli      # builds the Wails CLI into bin/wails (see the note below)
make build-mac      # produces build/bin/hearthpost.app
cp -R build/bin/hearthpost.app /Applications/
```

If the macOS firewall is on, the first incoming connection triggers the prompt "Do you want the application “hearthpost” to accept incoming network connections?".
Click "Allow".
If you click "Deny", hearthpost cannot receive anything. To change it later, allow hearthpost in System Settings → Network → Firewall → Options.

On macOS 15 or later, if you are asked for Local Network access, allow it.
You can change this in System Settings → Privacy & Security → Local Network.

### Windows

Run these in PowerShell one line at a time.
They build a Wails CLI with a newer x/tools into `bin\wails.exe` (see the note below for why).

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

This produces `build\bin\hearthpost.exe`. Put it anywhere and run it.
If Windows Defender Firewall asks for permission, allow "Private networks".

You can also build the Windows version on a Mac (`make build-windows`).

### Note on the Wails CLI

With Go 1.27, if you `go install` Wails CLI v2.13.0 as is, `wails build` stops with `internal error: package "context" without types was imported`.
The x/tools v0.44.0 that the CLI uses cannot read the Go 1.27 standard library (x/tools v0.52.0 can; this was confirmed).
The steps above and `make wails-cli` build the CLI with x/tools v0.52.0.

### Uninstall

1. Delete the app: `/Applications/hearthpost.app` on Mac, or wherever you put `hearthpost.exe` on Windows.
2. Delete the settings folder. It holds the device key and the list of paired devices.
   - Mac: `~/Library/Application Support/hearthpost`
   - Windows: `%AppData%\hearthpost`

Received files stay in the save folder.

## Installation prompt for LLM agents

Paste the following into a coding agent (such as Claude Code) as is.

```text
Build hearthpost (https://github.com/Rererr/hearthpost) from source and make it usable on this computer.

Check first:
- Whether the OS is macOS or Windows.
- That `go version` shows Go 1.25 or later and `node --version` shows Node.js 20 or later. If either is missing, explain how to install it and stop (do not install it yourself).
- On macOS, that `xcode-select -p` shows the Command Line Tools.

Steps:
1. In a working folder, run `git clone https://github.com/Rererr/hearthpost.git` and enter it.
2. macOS: run `make wails-cli`, then `make build-mac`. Confirm that build/bin/hearthpost.app exists.
   Windows (PowerShell): run the commands in the README "Windows" section in order. Confirm that build\bin\hearthpost.exe exists. PowerShell 5.1 has no `&&`, so run commands one at a time.
3. If `wails build` stops with "internal error: package "context" without types was imported", make sure you are using the CLI built with the newer x/tools (bin/wails).
4. Run `go test ./...` and confirm that the tests pass.
5. On macOS, ask the human whether you may copy the app to /Applications, and run `cp -R build/bin/hearthpost.app /Applications/` only if they agree.

Tell the human about these steps (an agent cannot do them):
- On the first incoming connection, if the macOS firewall asks to accept incoming connections, click "Allow". If Windows Defender Firewall asks, allow "Private networks".
- On macOS 15 or later, allow Local Network access if asked.
- Install and start hearthpost the same way on a second device, and confirm that each appears under "送り先" on the other.

Rules:
- Do not use sudo or administrator rights.
- Do not change anything outside the cloned repository and /Applications (only with permission). Normal Go and npm caches are fine.
- Do not change firewall or network settings.
```

## Development

```sh
make test         # build the UI, go vet, tests with -race
make build        # build for Mac (universal) and Windows
```

The networking core is `internal/transfer`, which does not depend on Wails.
The UI is React in `frontend/src`.

## License

MIT License. See [LICENSE](LICENSE).
