import * as Y from 'yjs'
import { WebsocketProvider } from 'y-websocket'

export interface CollabProvider {
  ydoc: Y.Doc
  provider: WebsocketProvider
  ytext: Y.Text
}

// A small palette so each participant gets a distinct, readable color.
const COLORS = [
  '#e6194b', '#3cb44b', '#4363d8', '#f58231', '#911eb4',
  '#46f0f0', '#f032e6', '#bcf60c', '#fabebe', '#008080',
]

function randomColor(): string {
  return COLORS[Math.floor(Math.random() * COLORS.length)]
}

function randomName(): string {
  return `User-${Math.floor(1000 + Math.random() * 9000)}`
}

// createCollabProvider wires a Yjs document to the Go relay over WebSocket.
// The URL derives from window.location.host so it works behind the dev proxy
// (vite :5173 → go :3000) and in production (same origin) without changes.
export function createCollabProvider(roomId: string): CollabProvider {
  const ydoc = new Y.Doc()
  const ytext = ydoc.getText('mermaid')

  const protocol = window.location.protocol === 'https:' ? 'wss' : 'ws'
  // y-websocket appends `/<roomId>` to the base URL, so the base path is /ws
  // and the room is passed as the room name argument.
  const wsBase = `${protocol}://${window.location.host}/ws`

  const provider = new WebsocketProvider(wsBase, roomId, ydoc)

  provider.awareness.setLocalStateField('user', {
    name: randomName(),
    color: randomColor(),
  })

  return { ydoc, provider, ytext }
}
