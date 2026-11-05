// 画面が使うバックエンド（Go 側の操作とイベント）の形。

export type Peer = {
  fingerprint: string
  name: string
  addr: string
  paired: boolean
  online: boolean
}

export type State = {
  name: string
  fingerprint: string
  saveDir: string
  port: number
  addresses: string[]
  startError: string
  peers: Peer[]
  os: string
  prompts: Prompt[]
}

export type Prompt = {
  id: string
  kind: 'pair' | 'offer'
  peerName: string
  peerFingerprint: string
  code: string
  fileCount: number
  totalSize: number
  topNames: string[] | null
  text: string
  expiresAt: number
}

export type TransferState = 'waiting' | 'running' | 'done' | 'failed' | 'canceled'

export type Progress = {
  id: string
  direction: 'send' | 'receive'
  peerName: string
  label: string
  done: number
  total: number
  state: TransferState
  error?: string
  saved?: string[]
  code?: string
}

export type Events = {
  peers: (peers: Peer[]) => void
  progress: (p: Progress) => void
  prompt: (p: Prompt) => void
  promptClosed: (id: string) => void
  text: (m: { from: string; text: string }) => void
  notice: (msg: string) => void
  pairCode: (code: string) => void
  fileDrop: (x: number, y: number, paths: string[]) => void
}

export interface Backend {
  getState(): Promise<State>
  setName(name: string): Promise<void>
  chooseSaveDir(): Promise<string>
  openSaveDir(): Promise<void>
  showInFolder(path: string): Promise<void>
  pickFiles(): Promise<string[]>
  pickFolder(): Promise<string>
  sendPaths(fp: string, paths: string[]): Promise<void>
  sendText(fp: string, text: string): Promise<void>
  readClipboard(): Promise<string>
  copyText(text: string): Promise<void>
  pair(fp: string): Promise<void>
  cancelPair(): Promise<void>
  confirmPairCode(same: boolean): Promise<void>
  unpair(fp: string): Promise<void>
  addManual(addr: string): Promise<Peer>
  respond(id: string, accept: boolean, pair: boolean): Promise<void>
  cancel(id: string): Promise<boolean>
  subscribe(ev: Events): () => void
}
