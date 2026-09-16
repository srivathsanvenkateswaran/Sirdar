import { useEffect, useRef, useState, type MouseEvent, type ReactNode } from 'react'
import ReactMarkdown, { type Components } from 'react-markdown'
import { markersForRef, parseRefs, type Marker as MarkerModel } from '../../lib/evidence'
import Button from '../../ui/button'
import Marker from '../../ui/marker'
import { CopyIcon } from './icons'

export interface MarkerHandlers {
  markers?: MarkerModel[]
  hotMarker?: string
  onMarker?: (id: string, event: MouseEvent<HTMLElement>) => void
}

/**
 * A `file:line` reference as the document draws it: code, with the marker
 * of the evidence item that cites the same lines beside it, so the claim in
 * the prose is one glance from the step that read the file.
 */
export function Ref({ text, markers = [], hotMarker, onMarker }: { text: string } & MarkerHandlers): JSX.Element {
  const ref = parseRefs(text)[0]
  const own = ref ? markersForRef(ref, markers) : []
  return (
    <span className="sn-ref" dir="ltr">
      {text}
      {own.map((m) => (
        <Marker key={m.id} id={m.id} size="sm" hot={m.id === hotMarker} onClick={onMarker} title={m.query} />
      ))}
    </span>
  )
}

/**
 * Plain text with its `file:line` references and its backticked spans
 * drawn as code, the references carrying their markers. Paragraph breaks
 * in the text become paragraphs.
 */
export function RefProse({
  text,
  className = 'sn-p',
  ...handlers
}: { text: string; className?: string } & MarkerHandlers): JSX.Element {
  const paragraphs = text
    .split(/\n\s*\n/)
    .map((p) => p.trim())
    .filter(Boolean)
  return (
    <>
      {paragraphs.map((p, i) => (
        <p key={i} className={className} dir="auto">
          {inline(p, handlers)}
        </p>
      ))}
    </>
  )
}

/** The inline run of one paragraph: code spans, references, bold. */
export function inline(text: string, handlers: MarkerHandlers): ReactNode[] {
  const out: ReactNode[] = []
  const parts = text.split(/(`[^`]+`|\*\*[^*]+\*\*)/)
  parts.forEach((part, i) => {
    if (part.startsWith('`') && part.endsWith('`') && part.length > 1) {
      const code = part.slice(1, -1)
      out.push(
        parseRefs(code).length > 0 && parseRefs(code)[0].text === code ? (
          <Ref key={i} text={code} {...handlers} />
        ) : (
          <code key={i}>{code}</code>
        ),
      )
      return
    }
    if (part.startsWith('**') && part.endsWith('**') && part.length > 4) {
      out.push(<b key={i}>{inline(part.slice(2, -2), handlers)}</b>)
      return
    }
    out.push(...withRefs(part, handlers, i))
  })
  return out
}

/** Splits plain text at its references, drawing each as a Ref. */
function withRefs(text: string, handlers: MarkerHandlers, key: number): ReactNode[] {
  const refs = parseRefs(text)
  if (refs.length === 0) return [text]
  const out: ReactNode[] = []
  let cursor = 0
  refs.forEach((ref, i) => {
    const at = text.indexOf(ref.text, cursor)
    if (at === -1) return
    if (at > cursor) out.push(text.slice(cursor, at))
    out.push(<Ref key={`${key}-${i}`} text={ref.text} {...handlers} />)
    cursor = at + ref.text.length
  })
  if (cursor < text.length) out.push(text.slice(cursor))
  return out
}

/**
 * Markdown at the note's measure, with `file:line` in code spans carrying
 * their markers. The note templates wrap Arabic in `<div dir="rtl">`, which
 * react-markdown would print as text; callers strip those first.
 */
export function Markdown({ text, ...handlers }: { text: string } & MarkerHandlers): JSX.Element {
  const components: Components = {
    code: ({ children, className }) => {
      const value = String(children ?? '')
      if (className) return <code className={className}>{children}</code>
      const ref = parseRefs(value)
      return ref.length > 0 && ref[0].text === value ? <Ref text={value} {...handlers} /> : <code>{children}</code>
    },
  }
  return (
    <div className="sn-md" dir="auto">
      <ReactMarkdown components={components}>{text}</ReactMarkdown>
    </div>
  )
}

/** A section head: the label with a dashed rule and a small mono tag at its start. */
export function Section({ title, tag, id, children }: { title: string; tag?: string; id?: string; children?: ReactNode }): JSX.Element {
  return (
    <>
      <div className="sn-sec" id={id} role="heading" aria-level={2} aria-label={title}>
        <span>{title}</span>
        {tag ? <span className="sn-sec__tag">{tag}</span> : null}
      </div>
      {children}
    </>
  )
}

const COPIED_MS = 2000

/** Copies `text` to the clipboard and says so for two seconds. */
export function CopyButton({ text, label = 'Copy' }: { text: string; label?: string }): JSX.Element {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle')
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current)
    },
    [],
  )
  async function copy(): Promise<void> {
    try {
      await navigator.clipboard?.writeText(text)
      setState('copied')
    } catch {
      setState('failed')
    }
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(() => setState('idle'), COPIED_MS)
  }
  return (
    <Button size="sm" icon={<CopyIcon />} onClick={() => void copy()}>
      {state === 'copied' ? 'Copied' : state === 'failed' ? 'Could not copy' : label}
    </Button>
  )
}

/**
 * The reply draft as a letter: to whom, a line saying it is a draft, the
 * text in its own language and direction, and Copy.
 */
export function ReplyLetter({
  to,
  language,
  text,
  note,
  actions,
}: {
  to?: string
  language?: string
  text: string
  note?: string
  actions?: ReactNode
}): JSX.Element {
  const rtl = language === 'ar' || language === 'he' || language === 'fa' || language === 'ur'
  return (
    <div className="sn-letter" data-testid="reply-letter">
      <div className="sn-letter__lh">
        {to ? (
          <>
            <span>To</span>
            <span className="sn-mono" dir="auto">
              {to}
            </span>
            <span>·</span>
          </>
        ) : null}
        <span>{note || 'A draft, not a sent reply. It promises nothing the ticket does not already record.'}</span>
      </div>
      <div className="sn-letter__body" dir={rtl ? 'rtl' : 'auto'} lang={language || undefined}>
        {text}
      </div>
      <div className="sn-letter__acts">
        <CopyButton text={text} />
        {actions}
      </div>
    </div>
  )
}
