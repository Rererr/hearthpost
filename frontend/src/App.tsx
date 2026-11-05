import { useCallback, useEffect, useRef, useState } from 'react'
import type { Backend, Peer, Progress, Prompt, State } from './backend'
import { errorMessage, formatBytes } from './format'

type Activity =
  | { kind: 'transfer'; id: string; p: Progress }
  | { kind: 'text'; id: string; from: string; text: string }

type Notice = { id: number; msg: string }

export default function App({ backend }: { backend: Backend }) {
  const [state, setState] = useState<State | null>(null)
  const [peers, setPeers] = useState<Peer[]>([])
  const [activities, setActivities] = useState<Activity[]>([])
  const [prompts, setPrompts] = useState<Prompt[]>([])
  const [notices, setNotices] = useState<Notice[]>([])
  const [textFor, setTextFor] = useState<{ peer: Peer; text: string; clipNote: string } | null>(null)
  const [pairing, setPairing] = useState<{ peer: Peer; code: string } | null>(null)
  const [dragging, setDragging] = useState(false)
  const [hoverFP, setHoverFP] = useState<string | null>(null)
  const noticeSeq = useRef(0)
  // 答えた問い合わせ。Go 側から消える前に状態を読み直しても、もう一度出さないため
  const answered = useRef(new Set<string>())
  const peersRef = useRef<Peer[]>([])
  peersRef.current = peers

  const notify = useCallback((msg: string) => {
    noticeSeq.current++
    const id = noticeSeq.current
    setNotices((ns) => [...ns, { id, msg }])
  }, [])

  const run = useCallback(
    async (f: () => Promise<unknown>) => {
      try {
        await f()
      } catch (e) {
        notify(errorMessage(e))
      }
    },
    [notify],
  )

  const reload = useCallback(async () => {
    try {
      const s = await backend.getState()
      setState(s)
      setPeers(s.peers ?? [])
      // 画面がイベントを受け始める前に届いた問い合わせを取りこぼさない
      setPrompts((ps) => [...ps, ...(s.prompts ?? []).filter((p) => !answered.current.has(p.id) && !ps.some((q) => q.id === p.id))])
    } catch (e) {
      notify(`状態を読み込めません: ${errorMessage(e)}`)
    }
  }, [backend, notify])

  const sendTo = useCallback(
    (fp: string, paths: string[]) => {
      const peer = peersRef.current.find((p) => p.fingerprint === fp)
      if (!peer) return notify('送り先が一覧にありません')
      if (!peer.online) return notify(`${peer.name} は見つかっていないため送れません`)
      void run(() => backend.sendPaths(fp, paths))
    },
    [backend, notify, run],
  )

  useEffect(() => {
    void reload()
    return backend.subscribe({
      peers: setPeers,
      progress: (p) =>
        setActivities((as) => {
          const i = as.findIndex((a) => a.kind === 'transfer' && a.id === p.id)
          if (i < 0) return [{ kind: 'transfer', id: p.id, p }, ...as]
          const next = as.slice()
          next[i] = { kind: 'transfer', id: p.id, p }
          return next
        }),
      prompt: (p) => setPrompts((ps) => (ps.some((q) => q.id === p.id) ? ps : [...ps, p])),
      promptClosed: (id) => setPrompts((ps) => ps.filter((p) => p.id !== id)),
      text: (m) => setActivities((as) => [{ kind: 'text', id: `t${Date.now()}${Math.random()}`, ...m }, ...as]),
      notice: notify,
      pairCode: (code) => setPairing((pg) => (pg ? { ...pg, code } : pg)),
      fileDrop: (x, y, paths) => {
        setDragging(false)
        setHoverFP(null)
        const el = document.elementFromPoint(x, y)?.closest<HTMLElement>('[data-fp]')
        if (!el?.dataset.fp) return notify('送り先の端末のカードの上で離してください')
        sendTo(el.dataset.fp, paths)
      },
    })
  }, [backend, reload, notify, sendTo])

  // ファイルをウィンドウの上へ持ってきた間だけ、どこで離せば送れるかを示す
  useEffect(() => {
    let depth = 0
    const enter = (e: DragEvent) => {
      if (!e.dataTransfer?.types.includes('Files')) return
      depth++
      setDragging(true)
    }
    const leave = () => {
      depth = Math.max(0, depth - 1)
      if (depth === 0) {
        setDragging(false)
        setHoverFP(null)
      }
    }
    const over = (e: DragEvent) => {
      const el = (e.target as Element | null)?.closest<HTMLElement>('[data-fp]')
      setHoverFP(el?.dataset.fp ?? null)
    }
    const drop = () => {
      depth = 0
    }
    window.addEventListener('dragenter', enter)
    window.addEventListener('dragleave', leave)
    window.addEventListener('dragover', over)
    window.addEventListener('drop', drop)
    return () => {
      window.removeEventListener('dragenter', enter)
      window.removeEventListener('dragleave', leave)
      window.removeEventListener('dragover', over)
      window.removeEventListener('drop', drop)
    }
  }, [])

  if (!state) return <div className="loading">読み込み中…</div>

  const startFailed = state.startError !== ''
  const prompt = prompts[0]

  return (
    <div className={`app ${dragging ? 'is-dragging' : ''}`}>
      <Header state={state} backend={backend} run={run} onChanged={reload} />

      {startFailed && (
        <div className="banner banner-error" role="alert">
          <strong>起動に失敗したため、送ることも受け取ることもできません。</strong>
          <p>{state.startError}</p>
        </div>
      )}

      <div className="notices" aria-live="polite">
        {notices.map((n) => (
          <div key={n.id} className="notice">
            <span>{n.msg}</span>
            <button className="icon" aria-label="閉じる" onClick={() => setNotices((ns) => ns.filter((x) => x.id !== n.id))}>
              ×
            </button>
          </div>
        ))}
      </div>

      <main className="columns">
        <section className="panel peers-panel" aria-labelledby="peers-h">
          <div className="panel-head">
            <h2 id="peers-h">送り先</h2>
            {dragging && <span className="drag-hint">送り先のカードの上で離してください</span>}
          </div>
          {peers.length === 0 ? (
            <EmptyPeers disabled={startFailed} />
          ) : (
            <ul className="peer-list">
              {peers.map((p) => (
                <PeerCard
                  key={p.fingerprint}
                  peer={p}
                  hovered={hoverFP === p.fingerprint}
                  disabled={startFailed}
                  onFiles={() =>
                    void run(async () => {
                      const paths = await backend.pickFiles()
                      if (paths.length > 0) sendTo(p.fingerprint, paths)
                    })
                  }
                  onFolder={() =>
                    void run(async () => {
                      const dir = await backend.pickFolder()
                      if (dir) sendTo(p.fingerprint, [dir])
                    })
                  }
                  onText={() =>
                    void (async () => {
                      try {
                        const clip = await backend.readClipboard()
                        setTextFor({ peer: p, text: clip, clipNote: clip ? 'クリップボードの内容を入れました。必要なら書き換えてください。' : '' })
                      } catch (e) {
                        setTextFor({ peer: p, text: '', clipNote: `クリップボードを読めなかったため空にしています（${errorMessage(e)}）。` })
                      }
                    })()
                  }
                  onPair={() => {
                    setPairing({ peer: p, code: '' })
                    void run(async () => {
                      await backend.pair(p.fingerprint)
                      notify(`${p.name} とペアリングしました。これ以降は確認なしで送り合えます。`)
                    }).finally(() => setPairing(null))
                  }}
                  onUnpair={() => {
                    if (window.confirm(`${p.name} とのペアリングを解除しますか？\n解除すると、${p.name} から届くたびに承認を求めます。\n${p.name} の側の登録は残るので、必要なら ${p.name} の画面でも解除してください。`)) {
                      void run(() => backend.unpair(p.fingerprint))
                    }
                  }}
                />
              ))}
            </ul>
          )}
          <ManualAdd disabled={startFailed} backend={backend} onError={notify} />
        </section>

        <section className="panel activity-panel" aria-labelledby="act-h">
          <div className="panel-head">
            <h2 id="act-h">やりとり</h2>
            {activities.some((a) => a.kind === 'text' || ['done', 'failed', 'canceled'].includes(a.p.state)) && (
              <button
                className="link"
                onClick={() => setActivities((as) => as.filter((a) => a.kind === 'transfer' && ['waiting', 'running'].includes(a.p.state)))}
              >
                終わったものを消す
              </button>
            )}
          </div>
          {activities.length === 0 ? (
            <p className="muted">送ったもの・受け取ったものがここに並びます。</p>
          ) : (
            <ul className="activity-list">
              {activities.map((a) =>
                a.kind === 'transfer' ? (
                  <TransferItem key={a.id} p={a.p} backend={backend} run={run} />
                ) : (
                  <TextItem key={a.id} from={a.from} text={a.text} backend={backend} run={run} />
                ),
              )}
            </ul>
          )}
        </section>
      </main>

      {prompt && (
        <PromptDialog
          key={prompt.id}
          prompt={prompt}
          backend={backend}
          run={run}
          onDone={() => {
            answered.current.add(prompt.id)
            setPrompts((ps) => ps.slice(1))
          }}
        />
      )}
      {textFor && (
        <TextDialog
          peer={textFor.peer}
          initial={textFor.text}
          note={textFor.clipNote}
          onClose={() => setTextFor(null)}
          onSend={(text) => {
            setTextFor(null)
            void run(() => backend.sendText(textFor.peer.fingerprint, text))
          }}
        />
      )}
      {pairing && (
        <PairingDialog
          peer={pairing.peer}
          code={pairing.code}
          onConfirm={(same) => void run(() => backend.confirmPairCode(same))}
          onCancel={() => void run(() => backend.cancelPair())}
        />
      )}
    </div>
  )
}

type RunFn = (f: () => Promise<unknown>) => Promise<void>

function Header({ state, backend, run, onChanged }: { state: State; backend: Backend; run: RunFn; onChanged: () => void }) {
  const [editing, setEditing] = useState(false)
  const [name, setName] = useState(state.name)
  return (
    <header className="header">
      <div className="me">
        <span className="me-label">この端末</span>
        {editing ? (
          <form
            className="name-form"
            onSubmit={(e) => {
              e.preventDefault()
              void run(async () => {
                await backend.setName(name)
                setEditing(false)
                onChanged()
              })
            }}
          >
            <input aria-label="この端末の名前" value={name} onChange={(e) => setName(e.target.value)} maxLength={40} autoFocus />
            <button type="submit" disabled={name.trim() === ''} title={name.trim() === '' ? '名前を入れてください' : undefined}>
              保存
            </button>
            <button type="button" className="link" onClick={() => (setName(state.name), setEditing(false))}>
              やめる
            </button>
          </form>
        ) : (
          <button className="me-name" onClick={() => setEditing(true)} title="クリックで名前を変える">
            {state.name} <span className="edit">変更</span>
          </button>
        )}
        <span className="muted small">
          {state.addresses.length > 0 ? `IP: ${state.addresses.join(' / ')}` : 'LAN に接続していません'}
        </span>
      </div>
      <div className="savedir">
        <span className="me-label">保存先</span>
        <span className="path" title={state.saveDir}>
          <bdi dir="ltr">{state.saveDir}</bdi>
        </span>
        <button className="link" onClick={() => void run(() => backend.openSaveDir())}>
          開く
        </button>
        <button className="link" onClick={() => void run(async () => (await backend.chooseSaveDir(), onChanged()))}>
          変更
        </button>
      </div>
    </header>
  )
}

function EmptyPeers({ disabled }: { disabled: boolean }) {
  if (disabled) return <p className="muted">起動に失敗しているため、端末を探していません。</p>
  return (
    <div className="empty">
      <p>
        <span className="pulse" aria-hidden /> LAN 内で hearthpost を起動している端末を探しています…
      </p>
      <details>
        <summary>しばらく待っても見つからないとき</summary>
        <ul>
          <li>相手の端末でも hearthpost を起動し、同じ Wi-Fi・同じルーターにつないでください。</li>
          <li>Windows では、ネットワークの種類を「プライベート」にしてください。「パブリック」では通信が止められます。</li>
          <li>Mac では「システム設定 → プライバシーとセキュリティ → ローカルネットワーク」で hearthpost を許可してください。</li>
          <li>ゲスト用 Wi-Fi やルーターの「AP 分離（プライバシーセパレーター）」、VPN は端末どうしの通信を止めます。</li>
          <li>それでも見つからなければ、下の欄に相手の IP アドレスを入れて追加してください。相手の画面の上部に表示されています。</li>
        </ul>
      </details>
    </div>
  )
}

function PeerCard(props: {
  peer: Peer
  hovered: boolean
  disabled: boolean
  onFiles: () => void
  onFolder: () => void
  onText: () => void
  onPair: () => void
  onUnpair: () => void
}) {
  const { peer: p, hovered, disabled } = props
  const offline = !p.online
  const blockReason = disabled ? '起動に失敗しているため送れません' : offline ? '見つかっていないため送れません' : undefined
  return (
    <li className={`peer ${offline ? 'is-offline' : ''} ${hovered ? 'is-hover' : ''}`} data-fp={p.online && !disabled ? p.fingerprint : undefined}>
      <div className="peer-head">
        <div>
          <div className="peer-name">{p.name}</div>
          <div className="muted small">{p.addr || '住所不明'}</div>
        </div>
        <span className={`badge ${p.paired ? 'badge-paired' : ''}`}>{p.paired ? 'ペアリング済み' : '未ペアリング'}</span>
      </div>
      <p className="peer-note small">
        {offline
          ? '見つかっていません。相手の hearthpost が起動しているか確かめてください。'
          : p.paired
            ? `${p.name} から届いたものは確認なしで受け取ります。`
            : `${p.name} から届くたびに承認を求めます。`}
      </p>
      <div className="peer-actions">
        <button onClick={props.onFiles} disabled={!!blockReason} title={blockReason}>
          ファイル
        </button>
        <button onClick={props.onFolder} disabled={!!blockReason} title={blockReason}>
          フォルダ
        </button>
        <button onClick={props.onText} disabled={!!blockReason} title={blockReason}>
          テキスト
        </button>
        <span className="spacer" />
        {p.paired ? (
          <button className="link" onClick={props.onUnpair} disabled={disabled}>
            解除
          </button>
        ) : (
          <button className="link" onClick={props.onPair} disabled={!!blockReason} title={blockReason}>
            ペアリング
          </button>
        )}
      </div>
      {!blockReason && <div className="drop-label">ここで離すと送ります</div>}
    </li>
  )
}

function ManualAdd({ disabled, backend, onError }: { disabled: boolean; backend: Backend; onError: (m: string) => void }) {
  const [addr, setAddr] = useState('')
  const [busy, setBusy] = useState(false)
  return (
    <form
      className="manual"
      onSubmit={async (e) => {
        e.preventDefault()
        setBusy(true)
        try {
          await backend.addManual(addr)
          setAddr('')
        } catch (err) {
          onError(errorMessage(err))
        } finally {
          setBusy(false)
        }
      }}
    >
      <label htmlFor="manual-addr" className="small muted">
        IP アドレスで追加
      </label>
      <input id="manual-addr" placeholder="例: 192.168.1.20" value={addr} onChange={(e) => setAddr(e.target.value)} disabled={disabled || busy} />
      <button type="submit" disabled={disabled || busy || addr.trim() === ''} title={addr.trim() === '' ? 'IP アドレスを入れてください' : undefined}>
        {busy ? '接続中…' : '追加'}
      </button>
    </form>
  )
}

const stateText: Record<Progress['state'], string> = {
  waiting: '相手の承認待ち',
  running: '転送中',
  done: '完了',
  failed: '失敗',
  canceled: '取り消し',
}

function TransferItem({ p, backend, run }: { p: Progress; backend: Backend; run: RunFn }) {
  const pct = p.total > 0 ? Math.floor((p.done / p.total) * 100) : p.state === 'done' ? 100 : 0
  const active = p.state === 'waiting' || p.state === 'running'
  return (
    <li className={`act act-${p.state}`}>
      <div className="act-head">
        <span className="dir" aria-label={p.direction === 'send' ? '送信' : '受信'}>
          {p.direction === 'send' ? '→' : '←'}
        </span>
        <span className="act-title">
          {p.direction === 'send' ? `${p.peerName} へ` : `${p.peerName} から`}：{p.label || '…'}
        </span>
        <span className={`state state-${p.state}`}>{stateText[p.state]}</span>
      </div>
      {p.state === 'waiting' && (
        <p className="small">
          {p.code ? (
            <>
              相手が承認を求めている場合は、相手の画面に確認コード <strong className="code">{p.code}</strong> が出ているか確かめてもらってください。
            </>
          ) : (
            '接続しています…'
          )}
        </p>
      )}
      {(p.state === 'running' || p.state === 'done') && p.total > 0 && (
        <div className="bar" role="progressbar" aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100}>
          <div style={{ width: `${pct}%` }} />
        </div>
      )}
      <div className="act-foot small">
        {p.total > 0 && (
          <span className="muted">
            {p.state === 'running' ? `${formatBytes(p.done)} / ` : ''}
            {formatBytes(p.total)}
          </span>
        )}
        {p.error && <span className="err">{p.error}</span>}
        <span className="spacer" />
        {active && (
          <button className="link" onClick={() => void run(() => backend.cancel(p.id))}>
            取り消す
          </button>
        )}
        {p.state === 'done' && p.saved && p.saved.length > 0 && (
          <button className="link" onClick={() => void run(() => backend.showInFolder(p.saved![0]))}>
            フォルダで表示
          </button>
        )}
      </div>
    </li>
  )
}

function TextItem({ from, text, backend, run }: { from: string; text: string; backend: Backend; run: RunFn }) {
  const [copied, setCopied] = useState(false)
  return (
    <li className="act act-text">
      <div className="act-head">
        <span className="dir" aria-label="受信">
          ←
        </span>
        <span className="act-title">{from} からテキスト</span>
      </div>
      <pre className="text-body">{text}</pre>
      <div className="act-foot small">
        <span className="spacer" />
        <button
          className="link"
          onClick={() =>
            void run(async () => {
              await backend.copyText(text)
              setCopied(true)
            })
          }
        >
          {copied ? 'コピーしました' : 'コピー'}
        </button>
      </div>
    </li>
  )
}

function useSecondsLeft(expiresAt: number) {
  const calc = () => Math.max(0, Math.ceil((expiresAt - Date.now()) / 1000))
  const [left, setLeft] = useState(calc)
  useEffect(() => {
    const t = setInterval(() => setLeft(calc()), 500)
    return () => clearInterval(t)
  })
  return left
}

// 新しく出た問い合わせのボタンを少しのあいだ押せなくする。
// 前の問い合わせが閉じた直後に次が同じ位置へ出ると、押しかけていたクリックや Enter で承認してしまうため。
const ARM_DELAY_MS = 700

function useArmed() {
  const [armed, setArmed] = useState(false)
  useEffect(() => {
    const t = setTimeout(() => setArmed(true), ARM_DELAY_MS)
    return () => clearTimeout(t)
  }, [])
  return armed
}

function PromptDialog({ prompt: p, backend, run, onDone }: { prompt: Prompt; backend: Backend; run: RunFn; onDone: () => void }) {
  const left = useSecondsLeft(p.expiresAt)
  const armed = useArmed()
  const answer = (accept: boolean, pair: boolean) => {
    onDone()
    void run(() => backend.respond(p.id, accept, pair))
  }
  const names = p.topNames ?? []
  return (
    <div className="modal-back">
      <div className="modal" role="dialog" aria-modal="true" aria-labelledby="prompt-h">
        {p.kind === 'pair' ? (
          <>
            <h3 id="prompt-h">{p.peerName} がペアリングを申し込んでいます</h3>
            <p>ペアリングすると、これ以降 {p.peerName} から届くものは確認なしで受け取ります。</p>
          </>
        ) : (
          <>
            <h3 id="prompt-h">{p.peerName} から届いています</h3>
            {names.length > 0 && (
              <>
                <p>
                  ファイル {p.fileCount} 個（{formatBytes(p.totalSize)}）
                </p>
                <ul className="names">
                  {names.slice(0, 5).map((n) => (
                    <li key={n}>{n}</li>
                  ))}
                  {names.length > 5 && <li className="muted">ほか {names.length - 5} 件</li>}
                </ul>
              </>
            )}
            {p.text && <pre className="text-body">{p.text.length > 300 ? `${p.text.slice(0, 300)}…` : p.text}</pre>}
          </>
        )}
        <div className="code-box">
          <span className="small muted">確認コード</span>
          <strong className="code big">{p.code}</strong>
          <span className="small muted">相手の画面と同じ数字か確かめてください。違えば断ってください。</span>
        </div>
        <div className="modal-actions">
          <button className="secondary" onClick={() => answer(false, false)}>
            断る
          </button>
          <span className="spacer" />
          {p.kind === 'offer' && (
            <button className="secondary" onClick={() => answer(true, true)} disabled={!armed}>
              受け取ってペアリング
            </button>
          )}
          <button className="primary" onClick={() => answer(true, false)} disabled={!armed}>
            {p.kind === 'pair' ? 'ペアリングする' : '受け取る'}
          </button>
        </div>
        <p className="small muted right">{left} 秒で自動的に断ります</p>
      </div>
    </div>
  )
}

function TextDialog(props: { peer: Peer; initial: string; note: string; onClose: () => void; onSend: (t: string) => void }) {
  const [text, setText] = useState(props.initial)
  const tooLong = new TextEncoder().encode(text).length > 1 << 20
  const empty = text.trim() === ''
  return (
    <div className="modal-back">
      <form
        className="modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="text-h"
        onSubmit={(e) => {
          e.preventDefault()
          if (!empty && !tooLong) props.onSend(text)
        }}
      >
        <h3 id="text-h">{props.peer.name} へテキストを送る</h3>
        {props.note && <p className="small muted">{props.note}</p>}
        <textarea value={text} onChange={(e) => setText(e.target.value)} rows={8} autoFocus />
        {tooLong && <p className="small err">1 MB を超えるテキストは送れません。ファイルにして送ってください。</p>}
        <div className="modal-actions">
          <button type="button" className="secondary" onClick={props.onClose}>
            やめる
          </button>
          <span className="spacer" />
          <button type="submit" className="primary" disabled={empty || tooLong} title={empty ? 'テキストが空です' : tooLong ? '大きすぎます' : undefined}>
            送る
          </button>
        </div>
      </form>
    </div>
  )
}

function PairingDialog(props: { peer: Peer; code: string; onConfirm: (same: boolean) => void; onCancel: () => void }) {
  const { peer, code } = props
  const [confirmed, setConfirmed] = useState(false)
  return (
    <div className="modal-back">
      <div className="modal" role="dialog" aria-modal="true" aria-labelledby="pairing-h">
        <h3 id="pairing-h">{peer.name} とペアリングしています</h3>
        <p>
          {peer.name} の画面にも確認コードが出ます。2 台の数字が同じか見比べて、同じなら「同じ数字です」を押してください。
          {peer.name} の側でも承認してもらうと、ペアリングが終わります。
        </p>
        <div className="code-box">
          <span className="small muted">確認コード</span>
          <strong className="code big">{code || '接続中…'}</strong>
          {confirmed && <span className="small muted">{peer.name} の承認を待っています…</span>}
        </div>
        <div className="modal-actions">
          <button className="secondary" onClick={props.onCancel}>
            取り消す
          </button>
          <span className="spacer" />
          {!confirmed && (
            <>
              <button className="secondary" onClick={() => props.onConfirm(false)} disabled={!code} title={code ? undefined : '確認コードが出るまで待ってください'}>
                違う数字です
              </button>
              <button
                className="primary"
                onClick={() => {
                  setConfirmed(true)
                  props.onConfirm(true)
                }}
                disabled={!code}
                title={code ? undefined : '確認コードが出るまで待ってください'}
              >
                同じ数字です
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
