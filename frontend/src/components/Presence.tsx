import { useEffect, useState } from 'react'
import type { Awareness } from 'y-protocols/awareness'
import styles from './Presence.module.css'

interface PresenceProps {
  awareness: Awareness
}

interface UserState {
  clientId: number
  name: string
  color: string
}

function initials(name: string): string {
  const parts = name.replace(/[-_]/g, ' ').split(' ').filter(Boolean)
  if (parts.length === 0) return '?'
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase()
  return (parts[0][0] + parts[1][0]).toUpperCase()
}

// Shows a colored avatar (dot + initials) for every connected participant,
// driven by the Yjs awareness protocol.
export default function Presence({ awareness }: PresenceProps) {
  const [users, setUsers] = useState<UserState[]>([])

  useEffect(() => {
    const update = () => {
      const next: UserState[] = []
      awareness.getStates().forEach((state, clientId) => {
        const user = (state as { user?: { name?: string; color?: string } }).user
        if (user) {
          next.push({
            clientId,
            name: user.name ?? 'Anonymous',
            color: user.color ?? '#888',
          })
        }
      })
      setUsers(next)
    }

    update()
    awareness.on('change', update)
    return () => awareness.off('change', update)
  }, [awareness])

  return (
    <div className={styles.presence}>
      {users.map((u) => (
        <div
          key={u.clientId}
          className={styles.avatar}
          style={{ backgroundColor: u.color }}
          title={u.name}
        >
          {initials(u.name)}
        </div>
      ))}
    </div>
  )
}
