import { useEffect, useRef, useState } from 'react'
import Button from '../../ui/button'
import { CopyIcon } from '../session/icons'
import { highlightCode } from './shikiHighlighter'

const COPIED_MS = 1500

/**
 * A block longer than this is shown folded to its first lines, with a toggle
 * for the rest. An agent's reply sometimes carries a whole data dump — the
 * 2026-10-05 OMNI-3420 reply ended in a 44-line JSON block — and a dump
 * should not push the reply's own words off the screen.
 */
export const COLLAPSE_LINES = 30

/**
 * A block with more characters than this folds as well, however few lines it
 * has: its lines wrap, and a handful of 800-character lines is as tall on
 * screen as a hundred short ones. The folded block is also capped in height
 * by its stylesheet, so wrapping cannot undo the fold.
 */
export const COLLAPSE_CHARS = 2400

/**
 * A fenced code block: the language and a Copy button above it, the code
 * below. Shiki's highlight lands as a second render once it resolves; the
 * plain `<pre><code>` it replaces is what every reader sees first, and
 * what a reader on an unknown language keeps seeing.
 */
export default function CodeBlock({ code, language }: { code: string; language?: string }): JSX.Element {
  const [html, setHtml] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)
  const [open, setOpen] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  const lines = code.split('\n')
  const foldable = lines.length > COLLAPSE_LINES || code.length > COLLAPSE_CHARS
  const folded = foldable && !open
  const shown = folded ? lines.slice(0, COLLAPSE_LINES).join('\n') : code

  useEffect(() => {
    setHtml(null)
    if (!language) return
    let cancelled = false
    highlightCode(shown, language).then((result) => {
      if (!cancelled) setHtml(result)
    })
    return () => {
      cancelled = true
    }
  }, [shown, language])

  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current)
    },
    [],
  )

  async function copy(): Promise<void> {
    try {
      await navigator.clipboard?.writeText(code)
      setCopied(true)
    } catch {
      setCopied(false)
    }
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(() => setCopied(false), COPIED_MS)
  }

  return (
    // Code is always left-to-right, even inside a message `.cmd` left as
    // `dir="auto"` picked up as right-to-left — without this, an RTL
    // paragraph flips the code lines to right-align under it.
    <div className="cmd-code" dir="ltr" data-testid="code-block" data-folded={folded ? '' : undefined}>
      <div className="cmd-code__head">
        <span className="cmd-code__lang">{language || 'text'}</span>
        <Button size="sm" variant="ghost" icon={<CopyIcon />} onClick={() => void copy()}>
          {copied ? 'Copied' : 'Copy'}
        </Button>
      </div>
      {html ? (
        <div className="cmd-code__body" dangerouslySetInnerHTML={{ __html: html }} />
      ) : (
        <pre className="cmd-code__body">
          <code>{shown}</code>
        </pre>
      )}
      {foldable ? (
        <button type="button" className="cmd-code__fold" onClick={() => setOpen(!open)}>
          {open ? 'Show less' : `Show all ${lines.length} lines`}
        </button>
      ) : null}
    </div>
  )
}
