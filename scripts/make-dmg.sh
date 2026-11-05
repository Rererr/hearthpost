#!/bin/bash
# 配布用の dmg を作る。開くと背景に入れ方と初回の開き方の案内が出て、hearthpost を Applications へドラッグできる。
# Finder の位置指定はアイコンが約 20px 下に出る（実測）ため、背景の配置より上の値を渡している。
# 背景とアイコンの位置は Finder にしか書けないので、AppleScript で Finder を操作する（初回は「Finder を制御」の許可を求められる）。
set -euo pipefail
cd "$(dirname "$0")/.."

app=build/bin/hearthpost.app
out=build/bin/hearthpost.dmg
vol=hearthpost
work=build/dmg
rw=build/dmg-rw.dmg

[ -d "$app" ] || { echo "$app がありません。先に make build-mac を実行してください" >&2; exit 1; }
if [ -d "/Volumes/$vol" ]; then
  echo "/Volumes/$vol がすでにマウントされています。取り出してからやり直してください（同名だと Finder が別のボリュームを操作してしまう）" >&2
  exit 1
fi

rm -rf "$work" "$rw" "$out"
mkdir -p "$work/.background"
cp -R "$app" "$work/"
ln -s /Applications "$work/Applications"
cp build/dmg-assets/background.tiff "$work/.background/background.tiff"
cp "build/dmg-assets/開き方の案内.webloc" "$work/"
SetFile -a E "$work/開き方の案内.webloc"
SetFile -a V "$work/.background"

hdiutil create -quiet -volname "$vol" -srcfolder "$work" -fs HFS+ -format UDRW -ov "$rw"
dev=$(hdiutil attach -readwrite -noverify -noautoopen "$rw" | awk '/\/Volumes\// {print $1; exit}')
trap 'hdiutil detach -quiet "$dev" 2>/dev/null || true' EXIT

osascript <<APPLESCRIPT
tell application "Finder"
  tell disk "$vol"
    open
    set current view of container window to icon view
    set toolbar visible of container window to false
    set statusbar visible of container window to false
    set bounds of container window to {200, 120, 840, 708}
    set opts to the icon view options of container window
    set arrangement of opts to not arranged
    set icon size of opts to 96
    set text size of opts to 12
    set background picture of opts to file ".background:background.tiff"
    set position of item "hearthpost.app" of container window to {160, 128}
    set position of item "Applications" of container window to {480, 128}
    set position of item "開き方の案内.webloc" of container window to {320, 436}
    set position of item ".background" of container window to {60, 470}
    update without registering applications
    delay 1
    close
  end tell
end tell
APPLESCRIPT

# マウント中に macOS が作る記録は配らない
rm -rf "/Volumes/$vol/.fseventsd"
sync
hdiutil detach -quiet "$dev"
trap - EXIT
hdiutil convert -quiet "$rw" -format UDZO -o "$out"
rm -rf "$work" "$rw"
echo "$out"
