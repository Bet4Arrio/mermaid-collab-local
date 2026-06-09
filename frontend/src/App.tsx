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
          {rooms.map((r) => (
            <li key={r.id}>
              <button className={styles.roomLink} onClick={() => onOpen(r.id)}>
                {r.title}
              </button>
              <span className={styles.muted}>
                {new Date(r.updated_at).toLocaleString()}
              </span>
              <button className={styles.delete} onClick={() => remove(r.id)}>
                ✕
              </button>
            </li>
          ))}
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

  useEffect(() => {
    const provider = collab.provider

    const onStatus = (e: { status: string }) =>
      setStatus(e.status as typeof status)
    provider.on('status', onStatus)

    // Seed a starter diagram once we're synced and the doc is still empty.
    const onSync = (isSynced: boolean) => {
      if (isSynced && collab.ytext.length === 0) {
        collab.ytext.insert(0, SAMPLE)
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

  return (
    <div className={styles.room}>
      <header className={styles.toolbar}>
        <button className={styles.back} onClick={onLeave}>
          ← Rooms
        </button>
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
