# Installing hearthpost (for people who are not computer experts)

[日本語](INSTALL.md)

This guide walks you through putting hearthpost on your home computers and sending your first file.
You do not need to type commands or know anything about programming.
It takes about 10 minutes per computer.

Read "What you need" and "Download" first, then only the chapter for your computer (Mac or Windows).
When it is installed, go on to "Send something for the first time".
If something goes wrong, look for your situation under "Troubleshooting" at the end.

The hearthpost screen is in Japanese only for now. This guide gives the Japanese label next to each button.

## What you need

- Two or more computers to install hearthpost on (Mac to Mac, Windows to Windows, and Mac to Windows all work)
- Both connected to the same home Wi-Fi (or the same router)
- macOS 10.13 or later, or Windows 10 or 11

hearthpost works only inside your home network.
Files you send never go to the internet or a cloud service.
In return, you cannot send between computers on different networks, such as one at home and one outside.

## Download

Open this page and, in the version at the top, click the file for your computer to download it.

https://github.com/Rererr/hearthpost/releases

| Computer | File to download |
|---|---|
| Mac | `hearthpost-mac.dmg` |
| Windows | `hearthpost-windows-setup.exe` |

The files are listed under "Assets" near the bottom of that version.
You do not need the files named "Source code"; they are for developers.

## Install on a Mac

### 1. Move the app to the Applications folder

1. Open the Downloads folder in Finder and double-click `hearthpost-mac.dmg`.
2. A window opens showing the "hearthpost" icon and an "Applications" folder. Drag "hearthpost" onto "Applications".
3. When done, click the eject button (⏏) next to "hearthpost" in the Finder sidebar. You can throw away the downloaded `hearthpost-mac.dmg`.

### 2. Open it for the first time

hearthpost is not signed by an Apple-verified developer (it is a free app published by an individual).
So the first time only, you need to tell your Mac that it is OK to open it.

1. Double-click "hearthpost" in the Applications folder.
2. If it says "Apple could not verify “hearthpost.app” is free of malware that may harm your Mac or compromise your privacy.", click "Done" to close it (do not click "Move to Trash"). This always appears for apps without a signature; it does not mean hearthpost is broken.
3. Open "System Settings" from the Apple menu at the top left, and choose "Privacy & Security" in the list on the left.
4. Scroll down on the right. Under "Security" there is a note about hearthpost and an "Open Anyway" button. Click it.
5. When asked again, click "Open Anyway" and enter your Mac password (or use Touch ID).

The hearthpost window opens.
From the second time on, just double-click it.

The exact wording and button names vary a little between macOS versions.
On macOS 14 and earlier, you can also Control-click "hearthpost" and choose "Open".

### 3. Answer "Allow" to the prompts

After it opens, one or both of these may appear.
hearthpost needs both to talk to the other computers in your home, so click "Allow".

- "“hearthpost” would like to find and connect to devices on your local network" → "Allow"
- "Do you want the application “hearthpost” to accept incoming network connections?" → "Allow"

If you click "Don't Allow" or "Deny", other computers may not show up, or you may not receive anything.
If that happened, see "Nothing arrives when someone sends to me" under Troubleshooting.

## Install on Windows

### 1. Open the installer

1. Open the Downloads folder in File Explorer and double-click `hearthpost-windows-setup.exe`.
2. If a blue screen says "Windows protected your PC", click "More info", then click "Run anyway".

This blue screen appears for apps that are not signed by a Microsoft-verified developer (it is a free app published by an individual).

### 2. Follow the installer

1. On "Welcome to hearthpost Setup", click "Next".
2. On "Choose Install Location", leave it as is and click "Install".
3. On "Completing hearthpost Setup", make sure "Run hearthpost" is checked and click "Finish".

hearthpost opens.
It also adds icons to the desktop and the Start menu; use them next time.
You do not need the administrator password (it is installed only for your current Windows user).

### 3. Answer "Allow" to the firewall prompt

If a "Windows Security Alert" window appears:

1. Make sure "Private networks" is checked.
2. You can leave "Public networks" unchecked.
3. Click "Allow access".

If you closed this window, or clicked "Cancel", see "Nothing arrives when someone sends to me" under Troubleshooting.

### 4. Set your home Wi-Fi to "Private"

When the Wi-Fi you are on is set to "Public", Windows blocks connections from other computers.
For your home Wi-Fi, set it to "Private".

1. Open "Settings" from the Start button and choose "Network & internet".
2. Choose "Wi-Fi" ("Ethernet" if wired), then click the name of the network you are connected to.
3. Under "Network profile type", choose "Private network".

Leave Wi-Fi at cafés, stations, and other public places set to "Public".

## Send something for the first time

### Name your computer

The name of this computer is shown at the top of the hearthpost window.
Click "変更" (change) next to it to rename it. A name your family recognizes, such as "Dad's Mac" or "Living room PC", helps.

### Wait for the other computer to appear under "送り先"

Open hearthpost on the other computer too.
After a few seconds, a card with its name appears under "送り先" (destinations).

### Send a file

Drag a file onto the card and let go to send it.
You can also press "ファイル" (files), "フォルダ" (folder), or "テキスト" (text) on the card and choose.

### Compare the confirmation code on the receiving computer

The receiving computer shows "(sender's name) から届いています" (has arrived from ...) and a 6-digit number (the confirmation code).
The sending computer shows a number too, so check that the two are the same.
If they are, press "受け取る" (accept).
If they differ, another computer may be in the middle, so press "断る" (decline).

### Open what you received

Received files go to your Downloads folder.
Use "保存先" (save folder) at the top of the window: "変更" changes the folder and "開く" opens it.
If a file with the same name already exists, nothing is overwritten; the new one is saved with a number, like "name (1)".

### Pair your family's computers

For computers you trust, such as your family's, pair them so that you no longer need to press "受け取る" every time.

1. Press "ペアリング" (pair) on the other computer's card.
2. Check that the 6-digit numbers on both screens are the same.
3. If they are, press "同じ数字です" (same number) on your screen, and have the other person press "ペアリングする" (pair) on theirs.

Pair only with computers you trust.
To stop, press "解除" (unpair) on the card. Unpairing affects only that computer, so ask the other person to unpair on theirs too.

## Troubleshooting

### The other computer does not appear under "送り先"

- Check that both computers are on the same Wi-Fi. Even in the same home, a computer on the router's "guest" Wi-Fi cannot reach the others.
- On Windows, check "Set your home Wi-Fi to 'Private'" above.
- If you use a VPN app, turn it off for now.
- If it still does not appear, type the number shown after "IP:" at the top of the other computer's window (for example `192.168.1.23`) into "IP アドレスで追加" (add by IP address) on your screen, and press "追加" (add).

### Nothing arrives when someone sends to me

You may have clicked "Don't Allow" or "Cancel" when you first opened hearthpost.

- Mac: In "System Settings" → "Network" → "Firewall" → "Options", set hearthpost to "Allow incoming connections". Also turn hearthpost on in "System Settings" → "Privacy & Security" → "Local Network".
- Windows: Open "Windows Security" from the Start button, choose "Firewall & network protection" → "Allow an app through firewall" → "Change settings", and check "Private" for hearthpost.

After fixing it, quit hearthpost and open it again.

### On a Mac, it says it cannot write to the save folder

If the save folder is "Documents" or "Desktop", your Mac may be blocking access.
Allow hearthpost in "System Settings" → "Privacy & Security" → "Files and Folders", or change the save folder back to "Downloads".

### Anything else

If this guide does not solve it, ask a question from "New issue" on this page (you need a GitHub account).

https://github.com/Rererr/hearthpost/issues

## Uninstall

- Mac: Move "hearthpost" in the Applications folder to the Trash.
- Windows: Open "Settings" from the Start button → "Apps" → "Installed apps", click "…" next to hearthpost, and choose "Uninstall" (on Windows 10: "Settings" → "Apps" → "Apps & features").

Received files stay in the save folder.
To also remove settings such as paired computers, delete this folder too:

- Mac: In Finder, choose Go → Go to Folder, enter `~/Library/Application Support/hearthpost`, and delete the folder that opens.
- Windows: Type `%AppData%\hearthpost` into the File Explorer address bar and delete the folder that opens.

---

Last updated: 2026-11-05
