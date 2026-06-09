import { useEffect, useRef } from 'react'
import * as Y from 'yjs'
import { EditorState } from '@codemirror/state'
import { EditorView, lineNumbers, keymap } from '@codemirror/view'
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import { markdown } from '@codemirror/lang-markdown'
import { yCollab } from 'y-codemirror.next'
import type { Awareness } from 'y-protocols/awareness'
import styles from './Editor.module.css'

interface EditorProps {
  ytext: Y.Text
  awareness: Awareness
  undoManager: Y.UndoManager
  // Fired (debounced upstream) whenever the document text changes.
  onChange: (value: string) => void
}

// CodeMirror 6 editor bound to a Yjs Text type via y-codemirror.next.
// Remote edits and cursors are synced through the shared awareness.
export default function Editor({ ytext, awareness, undoManager, onChange }: EditorProps) {
  const hostRef = useRef<HTMLDivElement>(null)
  const viewRef = useRef<EditorView | null>(null)

  useEffect(() => {
    if (!hostRef.current) return

    const state = EditorState.create({
      doc: ytext.toString(),
      extensions: [
        lineNumbers(),
        history(),
        markdown(),
        keymap.of([...defaultKeymap, ...historyKeymap, indentWithTab]),
        yCollab(ytext, awareness, { undoManager }),
        EditorView.lineWrapping,
        EditorView.theme({
          '&': { height: '100%', fontSize: '14px' },
          '.cm-scroller': { fontFamily: 'ui-monospace, monospace' },
        }),
      ],
    })

    const view = new EditorView({ state, parent: hostRef.current })
    viewRef.current = view

    // Push the initial value, then react to every shared-doc mutation.
    onChange(ytext.toString())
    const observer = () => onChange(ytext.toString())
    ytext.observe(observer)

    return () => {
      ytext.unobserve(observer)
      view.destroy()
      viewRef.current = null
    }
    // Bindings are created once per room; the props are stable refs.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ytext, awareness, undoManager])

  return <div ref={hostRef} className={styles.editor} />
}
