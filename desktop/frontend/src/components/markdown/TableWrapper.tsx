import { useRef, useState, type ReactNode } from 'react'
import Button from '../../ui/button'
import { CopyIcon } from '../session/icons'

const COPIED_MS = 1500

/** Escapes the one character that would otherwise split a Markdown table cell. */
function cell(text: string): string {
  return text.trim().replace(/\|/g, '\\|')
}

/** Rebuilds a GFM table from the live `<table>`, header row included. */
function asMarkdown(table: HTMLTableElement): string {
  const rows = [...table.rows].map((row) => [...row.cells].map((c) => cell(c.textContent ?? '')))
  if (rows.length === 0) return ''
  const [header, ...body] = rows
  const sep = header.map(() => '---')
  return [header, sep, ...body].map((row) => `| ${row.join(' | ')} |`).join('\n')
}

/**
 * A GFM table in a horizontally scrolling strip, with a Copy button that
 * turns the rendered table back into Markdown rather than copying the
 * pixel grid a reader cannot paste anywhere useful.
 */
export default function TableWrapper({ children }: { children?: ReactNode }): JSX.Element {
  const ref = useRef<HTMLTableElement>(null)
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  async function copy(): Promise<void> {
    const table = ref.current
    if (!table) return
    try {
      await navigator.clipboard?.writeText(asMarkdown(table))
      setCopied(true)
    } catch {
      setCopied(false)
    }
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(() => setCopied(false), COPIED_MS)
  }

  return (
    <div className="cmd-table">
      <div className="cmd-table__scroll">
        <table ref={ref}>{children}</table>
      </div>
      <Button size="sm" variant="ghost" icon={<CopyIcon />} onClick={() => void copy()}>
        {copied ? 'Copied' : 'Copy table'}
      </Button>
    </div>
  )
}
