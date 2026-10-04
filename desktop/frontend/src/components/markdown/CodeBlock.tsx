import { useEffect, useRef, useState } from 'react'
import Button from '../../ui/button'
import { CopyIcon } from '../session/icons'
import { highlightCode } from './shikiHighlighter'

const COPIED_MS = 1500

/**
 * A fenced code block: the language and a Copy button above it, the code
 * below. Shiki's highlight lands as a second render once it resolves; the
 * plain `<pre><code>` it replaces is what every reader sees first, and
 * what a reader on an unknown language keeps seeing.
 */
export default function CodeBlock({ code, language }: { code: string; language?: string }): JSX.Element {
  const [html, setHtml] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    setHtml(null)
    if (!language) return
    let cancelled = false
    highlightCode(code, language).then((result) => {
      if (!cancelled) setHtml(result)
    })
    return () => {
      cancelled = true
    }
  }, [code, language])

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
    <div className="cmd-code" dir="ltr" data-testid="code-block">
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
          <code>{code}</code>
        </pre>
      )}
    </div>
  )
}
