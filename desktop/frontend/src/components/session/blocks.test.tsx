import { fireEvent, render, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { IndexedEvent } from '../../lib/events'
import { parseNote } from '../../lib/note'
import { blockedFixture, fixDiff, fixFixture, triageFixture, type SessionFixture } from '../../store/fakeSession'
import AnswerCard from './AnswerCard'
import ChangesView from './ChangesView'
import ComposerStrip, { decisionText } from './ComposerStrip'
import { buildSessionModel, type SessionStep } from './model'
import NoteDocument from './NoteDocument'
import RunHeader, { badgeDetail, LayoutSwitcher } from './RunHeader'
import Stamp, { stepStamp } from './Stamp'
import ToolStep from './ToolStep'
import TurnGroup, { PathList } from './TurnGroup'

function indexed(f: SessionFixture): IndexedEvent[] {
  return f.events.map((event, i) => ({ index: i + 1, event }))
}

const triage = triageFixture()
const triageModel = buildSessionModel(triage.detail, indexed(triage), { diff: null })
const fix = fixFixture()
const fixModel = buildSessionModel(fix.detail, indexed(fix), { diff: fixDiff() })
const blocked = blockedFixture()
const blockedModel = buildSessionModel(blocked.detail, indexed(blocked), { diff: blocked.diff })

const stepBy = (object: string): SessionStep => {
  const step = triageModel.steps.find((s) => s.object === object)
  if (!step) throw new Error(`no step ${object}`)
  return step
}

describe('Stamp', () => {
  it('says the policy word a step earns, and nothing for a plain read', () => {
    expect(stepStamp(stepBy('git -C …/app log --stat --format=…'))).toEqual({ tone: 'deny', word: 'denied' })
    expect(stepStamp(stepBy('ls -la …/app'))).toEqual({ tone: 'list', word: 'allow-list' })
    expect(stepStamp(stepBy('ledger.go'))).toBeNull()
    expect(stepStamp(blockedModel.steps[blockedModel.steps.length - 1])).toEqual({ tone: 'wait', word: 'waiting on you' })
    const { container } = render(<Stamp tone="you">you</Stamp>)
    expect(container.querySelector('.sn-stamp')).toHaveAttribute('data-tone', 'you')
  })
})

describe('ToolStep', () => {
  it('collapsed is one line with the verb, the object, the result and its markers, and the line is the button', () => {
    const step = stepBy('rg -n "Return|restock"')
    const onToggle = vi.fn()
    render(<ToolStep step={step} markers={triageModel.markers.filter((m) => m.steps.includes(step.index))} open={false} onToggle={onToggle} onMarker={() => {}} />)
    const line = screen.getByTestId('tool-step')
    expect(line).toHaveTextContent('00:23')
    expect(line).toHaveTextContent('Ran rg -n "Return|restock" · 10 matches')
    expect(within(line).getByRole('button', { name: 'Marker E4' })).toBeInTheDocument()
    fireEvent.click(line)
    expect(onToggle).toHaveBeenCalledWith(step.index)
    expect(line).toHaveAttribute('aria-expanded', 'false')
  })

  it('a denied step is red with the policy reason under the line and no result', () => {
    const step = stepBy('date -j -f %Y-%m-%d 2026-09-12 +%A')
    render(<ToolStep step={step} open={false} onToggle={() => {}} />)
    const line = screen.getByTestId('tool-step')
    expect(line).toHaveAttribute('data-state', 'denied')
    expect(within(line).getByText('denied')).toBeInTheDocument()
    expect(line).toHaveTextContent('every segment of a pipeline or compound command has to match')
    expect(line).not.toHaveTextContent('lines')
  })

  it('expanded, an rg call shows its command verbatim and its output as a file:line table with a footer', () => {
    const step = stepBy('rg -n -i "partial|Quantity"')
    const onOpenInTools = vi.fn()
    render(<ToolStep step={step} open onToggle={() => {}} onOpenInTools={onOpenInTools} hotRefs={['ledger.go:33']} />)
    expect(screen.getByText('rg -n -i "partial|Quantity" --type go')).toHaveClass('sn-io__cmd')
    const table = screen.getByRole('table')
    expect(within(table).getAllByRole('row')).toHaveLength(12)
    expect(within(table).getByText('ledger.go:33').closest('tr')).toHaveAttribute('data-hit', 'true')
    expect(within(table).getByText('ledger.go:24').closest('tr')).not.toHaveAttribute('data-hit')
    expect(screen.getByText(/Output · 11 rows · .* · 80 ms/)).toBeInTheDocument()
    expect(screen.getByText('Search Go code for partial-return handling')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Open in Tools' }))
    expect(onOpenInTools).toHaveBeenCalledWith(step.index)
  })

  it('expanded, a file read keeps its line numbers', () => {
    render(<ToolStep step={stepBy('ledger.go')} open onToggle={() => {}} />)
    const lines = screen.getByRole('table', { name: 'File contents' })
    expect(lines.querySelectorAll('.sn-lines__n')).toHaveLength(32)
    expect(lines).toHaveTextContent('package ledger')
  })

  it('expanded, a failing test run is a block with the FAIL lines in the failed tone', () => {
    const step = fixModel.steps.find((s) => s.object === 'go test ./...')!
    render(<ToolStep step={step} open onToggle={() => {}} />)
    const bad = document.querySelectorAll('.sn-pre__ln[data-tone="bad"]')
    expect(bad.length).toBeGreaterThanOrEqual(2)
    expect(bad[0]).toHaveTextContent('Exit code 1')
    expect(screen.getByText(/FAIL · 1 test/)).toBeInTheDocument()
  })

  it('a waiting step says what it asks to run and the policy that stopped it', () => {
    const step = blockedModel.steps[blockedModel.steps.length - 1]
    render(<ToolStep step={step} open onToggle={() => {}} />)
    expect(screen.getByText('waiting on you')).toBeInTheDocument()
    expect(screen.getByText('Asks to run')).toBeInTheDocument()
    expect(screen.getAllByText('go test ./...').some((el) => el.classList.contains('sn-io__cmd'))).toBe(true)
    expect(screen.getByText(/The agent suggests allowing/)).toHaveTextContent('go test *')
    expect(screen.queryByText('Output')).toBeNull()
  })
})

describe('TurnGroup and PathList', () => {
  it('draws the rule, the annotation and the steps of a turn', () => {
    const turn = triageModel.path.find((p) => p.kind === 'turn' && p.turn.label === 'turn 1')
    if (!turn || turn.kind !== 'turn') throw new Error('no turn 1')
    render(<TurnGroup turn={turn.turn} markers={triageModel.markers} open={new Set()} onToggle={() => {}} />)
    const section = screen.getByRole('region', { name: 'turn 1' })
    expect(section).toHaveTextContent('turn 100:03')
    expect(within(section).getByText('List repository root files')).toHaveClass('sn-ann')
    expect(within(section).getAllByTestId('tool-step')).toHaveLength(1)
  })

  it('draws the steer as a you card and the rate-limit warnings as small lines between turns', () => {
    render(<PathList path={triageModel.path} markers={triageModel.markers} open={new Set()} onToggle={() => {}} />)
    const you = screen.getByTestId('you-card')
    expect(you).toHaveTextContent('you02:02· resumed the session')
    expect(you).toHaveTextContent('Re-check whether the partial-return path is also affected')
    expect(screen.getByText('claude 7-day window at 90%')).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'turns 3–8' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'resumed · turn 1' })).toBeInTheDocument()
    expect(screen.getAllByTestId('tool-step')).toHaveLength(15)
  })
})

describe('RunHeader', () => {
  it('draws key, kind, badge with its detail, title, provider and model, clock, turns, cost, and the switcher', () => {
    const onLayout = vi.fn()
    render(
      <RunHeader
        detail={triage.detail}
        title={triage.detail.title}
        show="tracker"
        notePath={triage.detail.notes[1]}
        switcher={<LayoutSwitcher value="document" onChange={onLayout} />}
      />,
    )
    expect(screen.getByRole('heading', { name: 'SBX-1' })).toBeInTheDocument()
    expect(screen.getByText('completed · note saved')).toBeInTheDocument()
    expect(screen.getByText('triage')).toBeInTheDocument()
    expect(screen.getByText('Product 00219 stock shows 1 more than the movement report')).toBeInTheDocument()
    expect(screen.getByRole('img', { name: 'Claude' })).toBeInTheDocument()
    expect(screen.getByText('opus')).toBeInTheDocument()
    expect(screen.getByText('17')).toBeInTheDocument()
    expect(screen.getByText('$1.02')).toBeInTheDocument()
    const group = screen.getByRole('radiogroup', { name: 'Session layout' })
    expect(within(group).getByRole('radio', { name: 'Document' })).toBeChecked()
    fireEvent.click(within(group).getByRole('radio', { name: 'Workbench' }))
    expect(onLayout).toHaveBeenCalledWith('workbench')
    expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull()
  })

  // Stopping a live run is the composer's Stop; the header never carries it.
  it('says blocked · waiting on you, and carries no Cancel; committed <sha> for a fix', () => {
    render(<RunHeader detail={blocked.detail} show="tracker" />)
    expect(screen.getByText('blocked · waiting on you')).toBeInTheDocument()
    expect(screen.getByText(/of 60 turns/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull()
    expect(badgeDetail(fix.detail)).toBe('committed f144936')
  })

  it('the gauges variant draws turns, minutes and cost as meters against the caps', () => {
    render(<RunHeader detail={triage.detail} show="tracker" variant="gauges" />)
    expect(screen.getByRole('meter', { name: 'turns' })).toHaveAttribute('aria-valuenow', '28')
    expect(screen.getByRole('meter', { name: 'cost' })).toHaveAttribute('aria-valuenow', '20')
    expect(screen.getByText('17 of 60')).toBeInTheDocument()
  })
})

describe('AnswerCard', () => {
  it('renders the answer as a finding: title, root cause with marked references, evidence, fix, letter and raw JSON', () => {
    const onMarker = vi.fn()
    const onGoToStep = vi.fn()
    render(
      <AnswerCard answer={triageModel.answer!} markers={triageModel.markers} onMarker={onMarker} steps={triageModel.steps} onGoToStep={onGoToStep} />,
    )
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(/Recording a customer return adds its quantity to stock twice/)
    expect(screen.getByRole('heading', { name: 'Root cause' })).toBeInTheDocument()
    // ledger.go:33 sits in E1's range, so the reference carries E1.
    const ref = screen.getAllByText('ledger.go:33').find((el) => el.classList.contains('sn-ref'))!
    fireEvent.click(within(ref).getByRole('button', { name: 'Marker E1' }))
    expect(onMarker).toHaveBeenCalledWith('E1', expect.anything())
    const evidence = screen.getByTestId('evidence-list')
    expect(evidence.querySelectorAll('.sn-evc')).toHaveLength(8)
    fireEvent.click(within(evidence).getAllByRole('button', { name: /^step 00:06/ })[0])
    expect(onGoToStep).toHaveBeenCalledWith(stepBy('ledger.go').index)
    expect(screen.getAllByText('code').some((el) => el.classList.contains('sd-badge'))).toBe(true)
    expect(screen.getByText('confidence high')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Proposed fix' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Blast radius' })).toBeInTheDocument()
    expect(screen.getByTestId('reply-letter')).toHaveTextContent('وعليكم السلام أستاذ أحمد')
    expect(screen.getByTestId('reply-letter').querySelector('.sn-letter__body')).toHaveAttribute('dir', 'rtl')
    expect(screen.getByText('Raw JSON')).toBeInTheDocument()
  })

  it('falls back to rows for fields the schema does not name', () => {
    render(<AnswerCard answer={{ verdict: 'fine', extra: { a: 1 } }} markers={[]} steps={[]} />)
    expect(screen.getByRole('heading', { name: 'Also in the answer' })).toBeInTheDocument()
    expect(screen.getByText('Verdict')).toBeInTheDocument()
    expect(screen.getByText('fine')).toBeInTheDocument()
  })
})

describe('NoteDocument', () => {
  const note = parseNote(triage.note)

  it('renders the metadata strip, the Newsreader title, the complaint beside its translation and the letter with Copy', () => {
    render(<NoteDocument note={note} detail={triage.detail} markers={triageModel.markers} steps={triageModel.steps} writtenAt="03:13" afterSteer contact="أحمد الفهد" />)
    const doc = screen.getByTestId('note-document')
    expect(within(doc).getByText('Customer')).toBeInTheDocument()
    expect(within(doc).getByText('متجر الفهد للأدوات المنزلية')).toBeInTheDocument()
    expect(within(doc).getByText('88341 · أحمد الفهد')).toBeInTheDocument()
    expect(within(doc).getByText('SBX-1 · normal')).toBeInTheDocument()
    expect(within(doc).getByRole('heading', { level: 1 })).toHaveClass('sn-note__h1')
    expect(within(doc).getByText(/written by the agent at 03:13 after your steer/)).toBeInTheDocument()
    const complaint = doc.querySelector('.sn-complaint')!
    expect(complaint.querySelector('.sn-complaint__ar')).toHaveAttribute('dir', 'rtl')
    expect(complaint.querySelector('.sn-complaint__ar')).toHaveTextContent('السلام عليكم ورحمة الله')
    expect(complaint.querySelector('.sn-complaint__en')).toHaveTextContent('Peace be upon you')
    expect(within(doc).getByText(/Tone: polite and calm/)).toBeInTheDocument()
    expect(within(doc).getByTestId('evidence-list').querySelectorAll('.sn-evc')).toHaveLength(8)
    expect(within(doc).getByRole('heading', { name: 'Open Questions' })).toBeInTheDocument()
    expect(within(doc).getByRole('heading', { name: 'Repro Steps' })).toBeInTheDocument()
    expect(within(doc).getByRole('button', { name: 'Copy' })).toBeInTheDocument()
    expect(within(doc).getByText(/A draft, not a sent reply/)).toBeInTheDocument()
  })

  it('marks a file:line in the root cause with the evidence that cites it, and lights the hot one', () => {
    render(<NoteDocument note={note} detail={triage.detail} markers={triageModel.markers} hotMarker="E1" steps={triageModel.steps} onMarker={() => {}} />)
    const refs = screen.getAllByText('ledger.go:33').filter((el) => el.classList.contains('sn-ref'))
    expect(refs.length).toBeGreaterThan(0)
    const chips = within(refs[0]).getAllByRole('button', { name: /Marker E/ })
    expect(chips.map((c) => c.textContent)).toEqual(['E1', 'E5'])
    expect(chips[0]).toHaveAttribute('data-hot', 'true')
    expect(document.querySelector('.sn-evc[data-marker="E1"]')).toHaveAttribute('data-hot', 'true')
  })
})

describe('ChangesView', () => {
  const common = {
    detail: fix.detail,
    diff: fixDiff(),
    loading: false,
    loadError: '',
    decisions: {},
    refusal: '',
    onKeep: vi.fn(),
    onDrop: vi.fn(),
    report: fixModel.report,
    checks: fixModel.checks,
    markers: fixModel.markers,
    onMarker: vi.fn(),
  }

  it('renders the strip, the commit subject, the checks with the pre-fix FAIL, the diff with Keep/Drop and C markers, the risks and Push', () => {
    render(<ChangesView {...common} drops={[{ path: 'ledger_test.go', hunk: 0, at: '00:49' }]} />)
    const view = screen.getByTestId('changes-view')
    expect(within(view).getByText('fix-sbx-1-recording-a-customer-return-adds-its-qua')).toBeInTheDocument()
    expect(within(view).getByText('f144936 · not pushed')).toBeInTheDocument()
    expect(within(view).getByText('2 · +15 −11')).toBeInTheDocument()
    expect(within(view).getByText('none · as proposed')).toBeInTheDocument()
    expect(within(view).getByRole('heading', { level: 1 })).toHaveTextContent("Apply a return's quantity to stock once in ApplyMovement")
    const checks = within(view).getByRole('list', { name: 'Checks' })
    expect(within(checks).getAllByRole('listitem')).toHaveLength(4)
    expect(within(checks).getByText('FAIL')).toBeInTheDocument()
    expect(within(checks).getAllByText('ok')).toHaveLength(3)
    expect(within(view).getAllByRole('button', { name: 'Keep' })).toHaveLength(3)
    expect(within(view).getAllByRole('button', { name: 'Drop' })).toHaveLength(3)
    expect(within(view).getByRole('button', { name: 'Marker C2' })).toBeInTheDocument()
    expect(within(view).getByText('dropped by you · 00:49')).toBeInTheDocument()
    expect(within(view).getByRole('heading', { name: 'Risks' })).toBeInTheDocument()
    fireEvent.click(within(view).getByRole('button', { name: /^Push/ }))
    expect(screen.getByTestId('push-command')).toHaveTextContent('git -C /repos/sirdar-sandbox/app/.sirdar/worktrees/20260915T121451Z-bf19 push -u origin fix-sbx-1-recording-a-customer-return-adds-its-qua')
    expect(within(view).getByRole('button', { name: 'Discard worktree' })).toBeDisabled()
    fireEvent.click(within(view).getAllByRole('button', { name: 'Drop' })[0])
    expect(common.onDrop).toHaveBeenCalledWith('ledger.go', 0)
  })

  it('while blocked, shows the change so far, the budget and that the checks wait on the answer', () => {
    render(
      <ChangesView
        {...common}
        detail={blocked.detail}
        diff={blocked.diff}
        report={undefined}
        checks={[]}
        markers={blockedModel.markers}
        pendingCommand="go test ./..."
      />,
    )
    const view = screen.getByTestId('changes-view')
    expect(within(view).getByText('4/60 turns · 00:11/20:00')).toBeInTheDocument()
    expect(within(view).getByText('1 · +14 −0')).toBeInTheDocument()
    expect(within(view).getByText('not yet run')).toBeInTheDocument()
    expect(within(view).getByRole('heading', { level: 1 })).toHaveTextContent('The change so far')
    expect(within(view).getByText(/which needs your answer below/)).toBeInTheDocument()
    expect(within(view).getByRole('button', { name: 'Marker C1' })).toBeInTheDocument()
    expect(within(view).queryByRole('button', { name: /^Push/ })).toBeNull()
  })

  it('gates a deviated fix on Accept and publish', () => {
    const onAcceptDeviation = vi.fn()
    render(<ChangesView {...common} detail={{ ...fix.detail, fix: { ...fix.detail.fix, deviation: 'Kept restock and removed the direct increment instead.' } }} onAcceptDeviation={onAcceptDeviation} />)
    expect(screen.getByText('reported · waiting on you')).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Deviation from the note' })).toHaveTextContent('Kept restock and removed the direct increment instead.')
    fireEvent.click(screen.getByRole('button', { name: 'Accept and publish' }))
    expect(onAcceptDeviation).toHaveBeenCalled()
  })

  it('says when the change cannot be read', () => {
    render(<ChangesView {...common} diff={null} loadError="the run has no change to show" />)
    expect(screen.getByText('the run has no change to show')).toBeInTheDocument()
  })
})

describe('ComposerStrip', () => {
  it('reads Steer with the resume handle, the playbooks and the provider once the run has completed', () => {
    const onSend = vi.fn()
    render(<ComposerStrip state={{ kind: 'steer' }} detail={triage.detail} busy={false} error="" onSend={onSend} sentCount={0} playbooks={3} lastSteer={triageModel.lastSteer} />)
    const strip = screen.getByTestId('composer-strip')
    expect(strip).toHaveAttribute('data-mode', 'steer')
    expect(within(strip).getByText('1e0a60c8')).toBeInTheDocument()
    expect(within(strip).getByText('Playbooks')).toBeInTheDocument()
    expect(within(strip).getByText('claude · opus')).toBeInTheDocument()
    // One row: the resume handle, playbooks, the model, and the mode with
    // the posture it runs under. No Access chip of its own.
    expect(within(strip).getByText('Triage · read-only')).toBeInTheDocument()
    expect(within(strip).queryByText('Access')).toBeNull()
    const box = within(strip).getByRole('textbox', { name: 'Steer' })
    expect(box).toHaveAttribute('placeholder', expect.stringContaining('Your last steer at 02:02'))
    const send = within(strip).getByRole('button', { name: /Steer/ })
    expect(send).toBeDisabled()
    fireEvent.change(box, { target: { value: 'Say it in one sentence.' } })
    fireEvent.click(send)
    expect(onSend).toHaveBeenCalledWith('Say it in one sentence.', undefined)
  })

  it('reads Reply while blocked, with the question, the rule and the decision segment, and sends the decision as words', () => {
    const onSend = vi.fn()
    render(<ComposerStrip state={blockedModel.composer} detail={blocked.detail} busy={false} error="" onSend={onSend} sentCount={0} />)
    const strip = screen.getByTestId('composer-strip')
    expect(strip).toHaveAttribute('data-mode', 'reply')
    expect(within(strip).getByText(/Waiting since 00:11/)).toBeInTheDocument()
    const q = within(strip).getByRole('group', { name: "The agent's question" })
    expect(q).toHaveTextContent('Run go test ./... in the worktree?')
    expect(q).toHaveTextContent('Run tests before the fix · this command requires approval · suggested rule go test * for this session')
    const seg = within(strip).getByRole('radiogroup', { name: 'Decision' })
    expect(within(seg).getAllByRole('radio').map((r) => r.textContent)).toEqual(['Allow once', 'Allow go test * this run', 'Deny'])
    const answer = within(strip).getByRole('button', { name: /Answer/ })
    expect(answer).toBeEnabled()
    fireEvent.click(within(seg).getByRole('radio', { name: 'Allow go test * this run' }))
    fireEvent.change(within(strip).getByRole('textbox', { name: 'Answer' }), { target: { value: 'Go ahead.' } })
    fireEvent.click(answer)
    expect(onSend).toHaveBeenCalledWith('Yes, and allow `go test *` for the rest of this session. Go ahead.', 'session')
    expect(decisionText('deny', 'go test *', '')).toBe('No, do not run it.')
    expect(decisionText('once', undefined, 'note')).toBe('Yes, run it once. note')
  })

  // Said once: the box carries it, the button stops the run, and no
  // heading or aside repeats the sentence.
  it('offers Stop while the run works, and says what typing there does exactly once', () => {
    const onStop = vi.fn()
    render(
      <ComposerStrip
        state={{ kind: 'running' }}
        detail={{ ...triage.detail, status: 'running' }}
        busy={false}
        error=""
        onSend={() => {}}
        sentCount={0}
        onStop={onStop}
        canStop
      />,
    )
    const strip = screen.getByTestId('composer-strip')
    expect(strip).toHaveAttribute('data-mode', 'running')
    expect(screen.getByRole('textbox')).toBeDisabled()
    expect(screen.getByRole('textbox')).toHaveAttribute('placeholder', 'Steer the run — it picks this up at its next turn')
    expect(screen.queryByText(/The run is still working/)).toBeNull()
    expect(screen.queryByRole('button', { name: /Steer/ })).toBeNull()
    const stop = screen.getByRole('button', { name: 'Stop the run' })
    fireEvent.click(stop)
    expect(onStop).toHaveBeenCalledTimes(1)
  })

  it('says Waiting for the run to finish when the provider cannot be steered', () => {
    render(
      <ComposerStrip
        state={{ kind: 'running' }}
        detail={{ ...triage.detail, status: 'running', provider: 'cursor' }}
        busy={false}
        error=""
        onSend={() => {}}
        sentCount={0}
      />,
    )
    expect(screen.getByRole('textbox')).toHaveAttribute('placeholder', 'Waiting for the run to finish')
  })
})
