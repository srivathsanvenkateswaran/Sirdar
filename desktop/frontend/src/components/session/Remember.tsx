import { useCallback, useEffect, useMemo, useState } from 'react'
import type { Transport } from '../../api/types'
import type { IndexedEvent } from '../../lib/events'
import type { ToastLink } from '../../store/appStore'
import { useOptionalStore } from '../../store/useAppStore'
import { withoutCode } from './useSessionModel'

/** Where a remembered message waits for review. */
export const PLAYBOOKS_LINK: ToastLink = { label: 'Open Playbooks', to: { name: 'settings', page: 'playbooks' } }

/**
 * Whether a log line is an operator message Remember this can keep. A grant
 * is a click, not words; an echo of a send has no line in the log yet for
 * the service to read back (its index is fractional); and an empty answer,
 * the one a model-limit Continue logs, says nothing to keep.
 */
export function canRemember(item: IndexedEvent): boolean {
  const kind = item.event.kind
  if (kind !== 'steer' && kind !== 'answer') return false
  if (!Number.isInteger(item.index)) return false
  const text = item.event.payload?.text
  return typeof text === 'string' && text.trim() !== ''
}

/** The button under an operator message that offers it to the playbooks. */
export function RememberButton({ busy, onRemember }: { busy: boolean; onRemember: () => void }): JSX.Element {
  return (
    <button type="button" className="sn-remember" disabled={busy} onClick={onRemember}>
      {busy ? 'Remembering…' : 'Remember this'}
    </button>
  )
}

export interface Remembering {
  /** The messages whose Remember this is in flight, by log index; 0 is the run's instruction. */
  busy: number[]
  remember: (index: number) => void
}

/**
 * One operator message, offered to the workspace's playbooks. Every layout
 * draws Remember this from here, so the three cannot disagree about what the
 * click sends or what the toast says. The feed numbers lines from 1, as
 * Service.Events does, so a message's index is the one the service reads
 * back, and the instruction, which is not in the log, is index 0.
 */
export function useRemember(transport: Transport, workspaceId: string, runId: string): Remembering {
  const store = useOptionalStore()
  const [busy, setBusy] = useState<number[]>([])

  // A click on one run says nothing about the next one opened. The list is
  // kept as it is when already empty, so opening a run costs no extra render.
  useEffect(() => setBusy((prev) => (prev.length === 0 ? prev : [])), [runId])

  const remember = useCallback(
    (index: number) => {
      setBusy((prev) => [...prev, index])
      transport
        .remember(workspaceId, runId, index)
        .then(
          // The toast's link says where to review it, so the text does not.
          (made) => store?.toast(`Suggested an addition to ${made.playbook}.`, 'info', PLAYBOOKS_LINK),
          (err: unknown) => store?.toast(withoutCode(err), 'error'),
        )
        .finally(() => setBusy((prev) => prev.filter((i) => i !== index)))
    },
    [transport, workspaceId, runId, store],
  )

  // One object per change, so a layout memoised on it redraws only when a click does.
  return useMemo(() => ({ busy, remember }), [busy, remember])
}
