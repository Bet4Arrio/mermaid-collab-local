import { useEffect, useRef, useState } from 'react'
import * as Y from 'yjs'
import Editor from './components/Editor'
import Preview from './components/Preview'
import Presence from './components/Presence'
import { createCollabProvider, type CollabProvider } from './lib/yjs'
import styles from './App.module.css'

interface RoomSummary {
  id: string
  title: string
  updated_at: string
}

// --- Lobby ------------------------------------------------------------------

function Lobby({ onOpen }: { onOpen: (id: string) => void }) {
  const [rooms, setRooms] = useState<RoomSummary[]>([])
  const [title, setTitle] = useState('')
  const [loading, setLoading] = useState(true)
  const [renamingId, setRenamingId] = useState<string | null>(null)
  const [renameValue, setRenameValue] = useState('')

  async function load() {
    setLoading(true)
    try {
      const res = await fetch('/api/rooms')
      setRooms(await res.json())
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
  }, [])

  async function create() {
    const name = title.trim()
    if (!name) return
    const res = await fetch('/api/rooms', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: name }),
    })
    const room = await res.json()
    setTitle('')
    onOpen(room.id)
  }

  async function remove(id: string) {
    await fetch(`/api/rooms/${id}`, { method: 'DELETE' })
    load()
  }

  function startRename(r: RoomSummary) {
    setRenamingId(r.id)
    setRenameValue(r.title)
  }

  function cancelRename() {
    setRenamingId(null)
    setRenameValue('')
  }

  async function saveRename(id: string) {
    const name = renameValue.trim()
    if (!name) return cancelRename()
    await fetch(`/api/rooms/${id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: name }),
    })
    cancelRename()
    load()
  }

  return (
    <div className={styles.lobby}>
      <h1>Mermaid Collab</h1>
      <div className={styles.createRow}>
        <input
          value={title}
          placeholder="New diagram title…"
          onChange={(e) => setTitle(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && create()}
        />
        <button onClick={create}>Create</button>
      </div>

      {loading ? (
        <p>Loading…</p>
      ) : rooms.length === 0 ? (
        <p className={styles.muted}>No diagrams yet. Create one above.</p>
      ) : (
        <ul className={styles.roomList}>
          {rooms.map((r) =>
            renamingId === r.id ? (
              <li key={r.id}>
                <input
                  className={styles.renameInput}
                  value={renameValue}
                  autoFocus
                  onChange={(e) => setRenameValue(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') saveRename(r.id)
                    if (e.key === 'Escape') cancelRename()
                  }}
                />
                <button className={styles.roomLink} onClick={() => saveRename(r.id)}>
                  ✓
                </button>
                <button className={styles.delete} onClick={cancelRename}>
                  ✕
                </button>
              </li>
            ) : (
              <li key={r.id}>
                <button className={styles.roomLink} onClick={() => onOpen(r.id)}>
                  {r.title}
                </button>
                <span className={styles.muted}>
                  {new Date(r.updated_at).toLocaleString()}
                </span>
                <button className={styles.rename} onClick={() => startRename(r)}>
                  ✎
                </button>
                <button className={styles.delete} onClick={() => remove(r.id)}>
                  ✕
                </button>
              </li>
            ),
          )}
        </ul>
      )}
    </div>
  )
}

// --- Room view --------------------------------------------------------------

const SAMPLE = `graph TD
  A[Start] --> B{Edit together}
  B --> C[Live preview]
  B --> D[Realtime sync]
`

function seedContent(title: string | null): string {
  if (!title) return SAMPLE
  return `---\ntitle: ${title}\n---\n${SAMPLE}`
}

function RoomView({ roomId, onLeave }: { roomId: string; onLeave: () => void }) {
  // The collab provider + undo manager are created exactly once per room and
  // live in refs, never in React state (per project rules).
  const collabRef = useRef<CollabProvider | null>(null)
  const undoRef = useRef<Y.UndoManager | null>(null)
  if (collabRef.current === null) {
    const collab = createCollabProvider(roomId)
    collabRef.current = collab
    undoRef.current = new Y.UndoManager(collab.ytext)
  }
  const collab = collabRef.current
  const undoManager = undoRef.current!

  const [source, setSource] = useState(collab.ytext.toString())
  const [status, setStatus] = useState<'connecting' | 'connected' | 'disconnected'>(
    'connecting',
  )
  const [title, setTitle] = useState<string | null>(null)
  const [editingTitle, setEditingTitle] = useState(false)
  const [titleDraft, setTitleDraft] = useState('')
  const [docEmptyOnSync, setDocEmptyOnSync] = useState(false)

  useEffect(() => {
    fetch(`/api/rooms/${roomId}`)
      .then((res) => res.json())
      .then((room) => setTitle(room.title))
  }, [roomId])

  async function saveTitle(next: string) {
    setEditingTitle(false)
    const name = next.trim()
    if (!name || name === title) return
    const res = await fetch(`/api/rooms/${roomId}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: name }),
    })
    if (res.ok) setTitle((await res.json()).title)
  }

  useEffect(() => {
    const provider = collab.provider

    const onStatus = (e: { status: string }) =>
      setStatus(e.status as typeof status)
    provider.on('status', onStatus)

    // Flag readiness to seed a starter diagram once we're synced and the
    // doc is still empty; the actual seed happens once the title has also
    // loaded (see the effect below).
    const onSync = (isSynced: boolean) => {
      if (isSynced && collab.ytext.length === 0) {
        setDocEmptyOnSync(true)
      }
    }
    provider.on('sync', onSync)

    return () => {
      provider.off('status', onStatus)
      provider.off('sync', onSync)
      undoManager.destroy()
      provider.destroy()
      collab.ydoc.destroy()
      collabRef.current = null
      undoRef.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Seed the starter diagram (with the room title in its frontmatter) once
  // the doc is confirmed empty and the title has loaded. Re-checks emptiness
  // in case another client seeded it in the meantime.
  useEffect(() => {
    if (docEmptyOnSync && title !== null && collab.ytext.length === 0) {
      collab.ytext.insert(0, seedContent(title))
    }
  }, [docEmptyOnSync, title])

  return (
    <div className={styles.room}>
      <header className={styles.toolbar}>
        <button className={styles.back} onClick={onLeave}>
          ← Rooms
        </button>
        {editingTitle ? (
          <input
            className={styles.titleInput}
            value={titleDraft}
            autoFocus
            onChange={(e) => setTitleDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') saveTitle(titleDraft)
              if (e.key === 'Escape') setEditingTitle(false)
            }}
          />
        ) : (
          <button
            className={styles.roomTitle}
            onClick={() => {
              setTitleDraft(title ?? '')
              setEditingTitle(true)
            }}
          >
            {title || 'Untitled'} ✎
          </button>
        )}
        <span className={styles.roomId}>room: {roomId}</span>
        <span className={`${styles.status} ${styles[status]}`}>{status}</span>
        <div className={styles.spacer} />
        <Presence awareness={collab.provider.awareness} />
      </header>
      <div className={styles.split}>
        <div className={styles.pane}>
          <Editor
            ytext={collab.ytext}
            awareness={collab.provider.awareness}
            undoManager={undoManager}
            onChange={setSource}
          />
        </div>
        <div className={styles.pane}>
          <Preview source={source} />
        </div>
      </div>
    </div>
  )
}

// --- App shell --------------------------------------------------------------

function currentRoom(): string {
  return window.location.hash.replace(/^#/, '')
}

export default function App() {
  const [roomId, setRoomId] = useState(currentRoom())

  useEffect(() => {
    const onHash = () => setRoomId(currentRoom())
    window.addEventListener('hashchange', onHash)
    return () => window.removeEventListener('hashchange', onHash)
  }, [])

  const open = (id: string) => {
    window.location.hash = id
  }
  const leave = () => {
    window.location.hash = ''
  }

  // Re-mount RoomView on room change via the key so the provider is recreated.
  return roomId ? (
    <RoomView key={roomId} roomId={roomId} onLeave={leave} />
  ) : (
    <Lobby onOpen={open} />
  )
}
