import { useEffect, useRef, useState } from 'react'
import mermaid from 'mermaid'
import styles from './Preview.module.css'

interface PreviewProps {
  source: string
}

const MIN_ZOOM = 0.25
const MAX_ZOOM = 10
const ZOOM_STEP = 0.1

const clampZoom = (z: number) => Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, z))

// Live Mermaid renderer. Debounces re-renders by 300ms (per project rules) to
// avoid thrashing on every keystroke, and surfaces parse errors inline.
// The diagram sits in a scrollable viewport: the user can scroll/drag to pan
// and ctrl/⌘+wheel (or the buttons) to zoom. Zoom is a CSS transform, which
// grows the scrollable overflow so panning reaches the whole diagram.
export default function Preview({ source }: PreviewProps) {
  const [svg, setSvg] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [zoom, setZoom] = useState(1)
  const idRef = useRef(0)
  const viewportRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const handle = setTimeout(async () => {
      const trimmed = source.trim()
      if (!trimmed) {
        setSvg('')
        setError(null)
        return
      }
      const renderId = `mermaid-${idRef.current++}`
      try {
        // parse() throws on invalid syntax before we attempt a full render.
        await mermaid.parse(trimmed)
        const { svg } = await mermaid.render(renderId, trimmed)
        setSvg(svg)
        setError(null)
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err))
      }
    }, 300)

    return () => clearTimeout(handle)
  }, [source])

  // ctrl/⌘+wheel zooms; a plain wheel keeps native scrolling (pan). Attached
  // natively (not via onWheel) so preventDefault is honored on a non-passive
  // listener. Re-attaches whenever the viewport mounts (svg/error toggles it).
  useEffect(() => {
    const el = viewportRef.current
    if (!el) return
    const onWheel = (e: WheelEvent) => {
      if (!e.ctrlKey && !e.metaKey) return
      e.preventDefault()
      setZoom((z) => clampZoom(z - e.deltaY * 0.002))
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [svg, error])

  // Drag to pan via the viewport's scroll position.
  const panRef = useRef<{ x: number; y: number; left: number; top: number } | null>(null)
  function onPointerDown(e: React.PointerEvent) {
    const el = viewportRef.current
    if (!el || e.button !== 0) return
    panRef.current = { x: e.clientX, y: e.clientY, left: el.scrollLeft, top: el.scrollTop }
    el.setPointerCapture(e.pointerId)
  }
  function onPointerMove(e: React.PointerEvent) {
    const el = viewportRef.current
    const start = panRef.current
    if (!el || !start) return
    el.scrollLeft = start.left - (e.clientX - start.x)
    el.scrollTop = start.top - (e.clientY - start.y)
  }
  function endPan(e: React.PointerEvent) {
    panRef.current = null
    viewportRef.current?.releasePointerCapture(e.pointerId)
  }

  if (error) {
    return (
      <div className={styles.preview}>
        <pre className={styles.error}>{error}</pre>
      </div>
    )
  }

  return (
    <div className={styles.preview}>
      <div
        ref={viewportRef}
        className={styles.viewport}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={endPan}
        onPointerLeave={endPan}
      >
        <div
          className={styles.diagram}
          style={{ transform: `scale(${zoom})` }}
          dangerouslySetInnerHTML={{ __html: svg }}
        />
      </div>
      <div className={styles.controls}>
        <button onClick={() => setZoom((z) => clampZoom(z - ZOOM_STEP))} title="Zoom out">
          −
        </button>
        <button onClick={() => setZoom(1)} title="Reset zoom">
          {Math.round(zoom * 100)}%
        </button>
        <button onClick={() => setZoom((z) => clampZoom(z + ZOOM_STEP))} title="Zoom in">
          +
        </button>
      </div>
    </div>
  )
}
