import type { FixInfo, RunDetail, SourcesSummary, Transport } from '../../api/types'
import type { Decision } from '../../components/session/ComposerStrip'
import type { SessionData } from '../../components/session/useSessionModel'
import type { SessionLayout } from '../../lib/sessionLayout'
import type { SessionsShow } from '../../lib/sessionsShow'

/**
 * What the dispatcher hands every layout. A layout draws from this and
 * nothing else — it never reads the transport itself — so Conversation,
 * Document and Workbench cannot disagree about what the run is or what a
 * send does. `data` is `useSessionModel`'s answer; the actions are the
 * dispatcher's, already wired to the transport and to the shell's job
 * pairing.
 */
export interface SessionLayoutProps {
  transport: Transport
  workspaceId: string
  runId: string
  detail: RunDetail
  data: SessionData
  /** The ticket's title, when the tracker's queue lists it. */
  title?: string
  /** The workspace's notes directory, so a filed note is named as the vault names it. */
  notesDir?: string
  sources?: SourcesSummary
  show: SessionsShow
  /** Under 1200: the header folds its figures into a title. */
  narrow: boolean
  live: boolean
  actions: {
    /** Answers a blocked run; `decision` is what the strip's segment held. */
    answer: (text: string, decision?: Decision) => void
    steer: (text: string) => void
    cancel: () => void
    acceptDeviation?: () => void
    openReview: () => void
  }
  pending: '' | 'answer' | 'steer' | 'cancel' | 'accept'
  /** What the last send came back with. */
  actionError: string
  /** Why the provider refused the last steer, while it holds. */
  steerRefusal: string
  /** How many sends went through, so a composer knows to clear. */
  sent: number
  /** Cancel's state: only a run this window started can be cancelled. */
  canCancel: boolean
  layout: SessionLayout
  onLayout: (layout: SessionLayout) => void
  fixInfo?: FixInfo
}
