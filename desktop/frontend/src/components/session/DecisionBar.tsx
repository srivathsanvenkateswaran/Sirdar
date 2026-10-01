import { useEffect, useId, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import type { DecisionAsk, Verdict } from '../../api/types'
import Button from '../../ui/button'
import './decision-bar.css'

export interface DecisionBarProps {
  ask: DecisionAsk
  /** A resume is in flight; every button waits and the shortcuts do nothing. */
  busy: boolean
  /** Answers the question: the verdict, and the reason a Deny was given. */
  onDecide: (verdict: Verdict, reason?: string) => void
}

/** The tool as a reader names it: MCP tools by their server and tool, not the mcp__ wire name. */
export function askToolLabel(ask: DecisionAsk): string {
  if (ask.kind === 'mcp') {
    const parts = ask.tool.split('__').filter(Boolean)
    if (parts.length >= 3) return `${parts[1]} · ${parts.slice(2).join('__')}`
  }
  return ask.tool
}

/**
 * The call in one line: the command, the URL, the path. An MCP tool's
 * arguments are its call, but the tool's own name says more at a glance,
 * so its arguments ride along only on hover.
 */
export function askCall(ask: DecisionAsk): string {
  return ask.kind === 'mcp' ? '' : ask.summary
}

/**
 * Why the policy asked, in a few words a reader can act on. The policy's own
 * reason is a paragraph naming every allow-list entry; it stays on hover.
 */
export function askWhy(ask: DecisionAsk): string {
  switch (ask.kind) {
    case 'bash':
      return 'Not on permissions.bash'
    case 'mcp':
      return 'Not allowed by permissions.mcp'
    case 'fetch':
      return 'Host not on permissions.fetch'
    default:
      return 'Outside what this run may use'
  }
}

/** What "allow for this run" adds, for the button's tooltip. */
function patternsHint(ask: DecisionAsk): string {
  const patterns = ask.patterns ?? []
  if (patterns.length === 0) return 'Allow this call and any like it until the run ends'
  return `Allow this call, and any later ${patterns.join(', ')}, until the run ends`
}

/** True when a key press is typing into a field that has something in it. */
function editingText(target: EventTarget | null): boolean {
  const el = target as HTMLInputElement | HTMLTextAreaElement | null
  if (!el || !el.tagName) return false
  const tag = el.tagName.toLowerCase()
  return (tag === 'textarea' || tag === 'input') && el.value !== ''
}

/**
 * The decision bar: a blocked run's permission question, answered with a
 * click instead of a sentence.
 *
 * One line says what the agent wants — the tool, then the call in the mono
 * face, cut to the bar's width with the whole of it and the policy's reason
 * on hover — and three buttons answer it. Allow once is the screen's one
 * filled control while the bar is up; Allow for this run also lets later
 * calls matching the same pattern through, and Deny opens a one-line reason
 * the agent is shown. The composer's text box under the bar stays for an
 * answer in words, which runs nothing.
 *
 * ⌘⏎ (Ctrl+Enter) is Allow once from anywhere on the screen. ⌘⌫
 * (Ctrl+Backspace) is Deny, except in a field that has text in it, where
 * the key keeps its own meaning of deleting what was typed.
 */
export default function DecisionBar({ ask, busy, onDecide }: DecisionBarProps): JSX.Element {
  const [denying, setDenying] = useState(false)
  const [reason, setReason] = useState('')
  const reasonId = useId()
  const reasonRef = useRef<HTMLInputElement | null>(null)

  // A new question starts clean.
  useEffect(() => {
    setDenying(false)
    setReason('')
  }, [ask.tool, ask.summary])

  useEffect(() => {
    if (denying) reasonRef.current?.focus()
  }, [denying])

  // The latest state, for the window listener registered once.
  const latest = useRef({ busy, denying, reason, onDecide })
  latest.current = { busy, denying, reason, onDecide }

  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || e.altKey || e.shiftKey) return
      const now = latest.current
      if (e.key === 'Enter') {
        // Ahead of the composer's own Enter, which would send the box.
        e.preventDefault()
        e.stopPropagation()
        if (!now.busy) now.onDecide('allow')
        return
      }
      if (e.key === 'Backspace') {
        if (editingText(e.target)) return
        e.preventDefault()
        e.stopPropagation()
        if (now.busy) return
        if (now.denying) now.onDecide('deny', now.reason.trim() || undefined)
        else setDenying(true)
      }
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [])

  const tool = askToolLabel(ask)
  const call = askCall(ask)
  const full = [`${ask.tool}${ask.summary ? ` ${ask.summary}` : ''}`, ask.reason].filter(Boolean).join('\n\n')

  const confirmDeny = (e: FormEvent) => {
    e.preventDefault()
    if (!busy) onDecide('deny', reason.trim() || undefined)
  }
  const onReasonKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Escape') {
      e.preventDefault()
      setDenying(false)
    }
  }

  return (
    <div className="sn-decide" role="group" aria-label="The agent asks to run" data-kind={ask.kind} data-testid="decision-bar">
      <div className="sn-decide__q" title={full}>
        <span className="sn-decide__tool" dir="ltr">
          {tool}
        </span>
        {call ? (
          <code className="sn-decide__call" dir="ltr">
            {call}
          </code>
        ) : null}
        <span className="sn-decide__why">{askWhy(ask)}</span>
      </div>
      {denying ? (
        // The three answers give way to the reason: one row, the field and
        // the two ways out of it.
        <form className="sn-decide__deny" onSubmit={confirmDeny}>
          <label className="visually-hidden" htmlFor={reasonId}>
            Why not
          </label>
          <input
            id={reasonId}
            ref={reasonRef}
            className="sn-decide__reason"
            value={reason}
            dir="auto"
            placeholder="Why not (optional) — the agent is told"
            onChange={(e) => setReason(e.target.value)}
            onKeyDown={onReasonKey}
          />
          <Button type="submit" variant="primary" busy={busy} shortcut="↵">
            Deny
          </Button>
          <Button variant="ghost" onClick={() => setDenying(false)}>
            Cancel
          </Button>
        </form>
      ) : (
        <div className="sn-decide__actions">
          <Button
            variant="primary"
            busy={busy}
            shortcut="⌘⏎"
            aria-keyshortcuts="Meta+Enter Control+Enter"
            title="Run this call once (⌘⏎)"
            onClick={() => onDecide('allow')}
          >
            Allow once
          </Button>
          <Button busy={busy} title={patternsHint(ask)} onClick={() => onDecide('allow_run')}>
            Allow for this run
          </Button>
          <Button
            busy={busy}
            shortcut="⌘⌫"
            aria-keyshortcuts="Meta+Backspace Control+Backspace"
            title="Refuse this call for the rest of the run, with a reason if you want (⌘⌫)"
            onClick={() => setDenying(true)}
          >
            Deny
          </Button>
        </div>
      )}
    </div>
  )
}
