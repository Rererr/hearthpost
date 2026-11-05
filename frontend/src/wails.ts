import * as Go from '../wailsjs/go/main/App'
import { EventsOn, OnFileDrop, OnFileDropOff } from '../wailsjs/runtime/runtime'
import type { Backend, Peer, Progress, Prompt, State } from './backend'

// Wails が生成する型はクラスで、画面側の型（プレーンなオブジェクト）と同じ形なのでそのまま渡す
export const wailsBackend: Backend = {
  getState: () => Go.GetState() as Promise<State>,
  setName: Go.SetName,
  chooseSaveDir: Go.ChooseSaveDir,
  openSaveDir: Go.OpenSaveDir,
  showInFolder: Go.ShowInFolder,
  pickFiles: async () => (await Go.PickFiles()) ?? [],
  pickFolder: Go.PickFolder,
  sendPaths: Go.SendPaths,
  sendText: Go.SendText,
  readClipboard: Go.ReadClipboard,
  copyText: Go.CopyText,
  pair: Go.Pair,
  cancelPair: Go.CancelPair,
  confirmPairCode: Go.ConfirmPairCode,
  unpair: Go.Unpair,
  addManual: (addr) => Go.AddManual(addr) as Promise<Peer>,
  respond: Go.Respond,
  cancel: Go.Cancel,
  subscribe(ev) {
    const offs = [
      EventsOn('peers', (p: Peer[]) => ev.peers(p ?? [])),
      EventsOn('progress', (p: Progress) => ev.progress(p)),
      EventsOn('prompt', (p: Prompt) => ev.prompt(p)),
      EventsOn('prompt-closed', (id: string) => ev.promptClosed(id)),
      EventsOn('text', (m: { from: string; text: string }) => ev.text(m)),
      EventsOn('notice', (m: string) => ev.notice(m)),
      EventsOn('pair-code', (c: string) => ev.pairCode(c)),
    ]
    // ドロップ先の判定は画面側でする。true にすると、カードの外で離したときに何も呼ばれず理由を出せない
    OnFileDrop(ev.fileDrop, false)
    return () => {
      offs.forEach((off) => off())
      OnFileDropOff()
    }
  },
}
