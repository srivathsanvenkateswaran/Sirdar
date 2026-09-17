import { useCallback, useEffect, useMemo, useRef, useState, type MouseEvent } from 'react'
import AnswerCard from '../../components/session/AnswerCard'
import BundlePane from '../../components/session/BundlePane'
import ChangesView from '../../components/session/ChangesView'
import ComposerStrip from '../../components/session/ComposerStrip'
import { BundleIcon, OpenIcon, ToolsIcon } from '../../components/session/icons'
import type { SessionModel } from '../../components/session/model'
import NoteDocument, { MetaCell } from '../../components/session/NoteDocument'
import ToolsPane from '../../components/session/ToolsPane'
import { rowsOf } from '../../components/session/toolRows'
import { PathList } from '../../components/session/TurnGroup'
import { markersForStep, parseRefs } from '../../lib/evidence'
import { costOrUnknown } from '../../lib/format'
import { noteName } from '../../lib/review'
import Button from '../../ui/button'
import Drawer from '../../ui/drawer'
import Toggle from '../../ui/toggle'
import type { SessionLayoutProps } from './layoutProps'
import '../../components/session/session.css'

type DrawerName = 'bundle' | 'tools' | null

/** The steps a marker names, in path order. */
function stepsOf(model: SessionModel, id: string): number[] {
  return model.markers.find((m) => m.id === id)?.steps ?? []
}

/** Scrolls `el` into the middle of its scroll container; jsdom has no layout, so it may be a no-op. */
function reveal(el: Element | null | undefined): void {
  el?.scrollIntoView?.({ block: 'center', behavior: 'smooth' })
}

/**
 * Layout B, "the note is the work": the deliverable as a document in the
 * centre, the path the agent took as a timeline on the left grouped by turn,
 * Bundle and Tools as drawers over the path, and the strip under the
 * document — Reply when the run is blocked, Steer when it has finished,
 * Stop while it works.
 *
 * The audit is the markers: every evidence item in the document carries
 * E1…En, the same marker sits on the step that produced it, and clicking
 * either scrolls to and lights the other. For a fix run the document is the
 * change, with C markers tying each hunk to the edit that wrote it.
 */
export default function SessionDocument(props: SessionLayoutProps): JSX.Element {
  const { transport, workspaceId, detail, data, title, notesDir, live, actions, pending, actionError, steerRefusal, sent, canCancel } = props
  const { model, note, bundle, changes, drops, everything, setEverything } = data
  // The record's model, else the one the log names, so the strip never says "model unknown" for a run that did say.
  const named = useMemo(() => (detail.model || !model.model ? detail : { ...detail, model: model.model }), [detail, model.model])
  const [drawer, setDrawer] = useState<DrawerName>(null)
  const [open, setOpen] = useState<ReadonlySet<number>>(() => new Set())
  const [hot, setHot] = useState<string | undefined>()
  const [selectedStep, setSelectedStep] = useState<number | undefined>()
  const pathRef = useRef<HTMLDivElement | null>(null)
  const docRef = useRef<HTMLDivElement | null>(null)
  const isFix = detail.kind === 'fix'
  const toolRows = useMemo(() => rowsOf(model.steps), [model.steps])
  // Only the desktop shell can reveal a folder, so a browser gets no item
  // at all and the pane never shows a path it cannot act on.
  const openBundleFolder = useMemo(
    () => (transport.openRunDir ? () => void transport.openRunDir!(workspaceId, detail.runId) : undefined),
    [transport, workspaceId, detail.runId],
  )

  // What this screen holds about a run is about that run alone.
  useEffect(() => {
    setDrawer(null)
    setOpen(new Set())
    setHot(undefined)
    setSelectedStep(undefined)
  }, [props.runId])

  // The call the run is waiting on stays expanded, as the mock's S2 shows.
  const pendingIndex = model.composer.kind === 'reply' ? model.composer.pending?.index : undefined
  useEffect(() => {
    if (pendingIndex === undefined) return
    setOpen((prev) => (prev.has(pendingIndex) ? prev : new Set([...prev, pendingIndex])))
  }, [pendingIndex])

  const toggleStep = useCallback((index: number) => {
    setOpen((prev) => {
      const next = new Set(prev)
      if (next.has(index)) next.delete(index)
      else next.add(index)
      return next
    })
  }, [])

  const goToStep = useCallback((index: number) => {
    setDrawer(null)
    setOpen((prev) => (prev.has(index) ? prev : new Set([...prev, index])))
    setSelectedStep(index)
    // After the drawer has closed and the step has opened.
    requestAnimationFrame(() => reveal(pathRef.current?.querySelector(`[data-step="${index}"], [data-index="${index}"]`)))
  }, [])

  /**
   * A marker was clicked. From the document it finds the step and opens
   * it; from the path it finds the evidence callout or the hunk. Either way
   * the marker lights on both sides until another is clicked.
   */
  const onMarker = useCallback(
    (id: string, event: MouseEvent<HTMLElement>) => {
      setHot(id)
      const fromPath = Boolean((event.currentTarget as HTMLElement).closest('.sn-path, .sd-drawer'))
      if (fromPath) {
        requestAnimationFrame(() => reveal(docRef.current?.querySelector(`[data-marker="${id}"], [data-hunk-marker="${id}"]`)))
        return
      }
      const steps = stepsOf(model, id)
      if (steps.length > 0) goToStep(steps[0])
    },
    [model, goToStep],
  )

  const openInTools = useCallback((index: number) => {
    setSelectedStep(index)
    setDrawer('tools')
  }, [])

  const hotRefs = useMemo(() => {
    if (!hot) return []
    const marker = model.markers.find((m) => m.id === hot)
    return marker ? marker.refs.map((r) => `${r.file}:${r.from}`) : []
  }, [hot, model.markers])
  const hotSteps = useMemo(() => (hot ? stepsOf(model, hot) : []), [hot, model])

  const notePath = note.path
  const outputs = model.steps.filter((s) => s.kind === 'output')
  const writtenAt = outputs[outputs.length - 1]?.at
  const afterSteer = Boolean(model.lastSteer)
  const playbooks = bundle.parsed?.playbooks.length ?? 0
  const contact = bundle.parsed?.thread.find((m) => m.role === 'customer')?.author
  const helpdeskUrl = bundle.parsed?.ticket['Helpdesk URL']
  const replyActions = helpdeskUrl ? (
    <Button variant="ghost" size="sm" onClick={() => window.open(helpdeskUrl, '_blank', 'noreferrer')}>
      Open desk {detail.helpdeskKey || ''} <OpenIcon />
    </Button>
  ) : undefined

  const composerError = model.composer.kind === 'disabled' ? '' : actionError
  const sendBusy = pending === 'answer' || pending === 'steer'
  const composerState = steerRefusal ? ({ kind: 'disabled', reason: steerRefusal } as const) : model.composer

  let document: JSX.Element
  if (isFix) {
    document = (
      <ChangesView
        detail={detail}
        diff={changes.diff}
        loading={changes.loading}
        loadError={changes.error}
        decisions={changes.decisions}
        dropping={changes.dropping}
        refusal={changes.refusal}
        onKeep={changes.keep}
        onDrop={changes.drop}
        report={model.report}
        checks={model.checks}
        markers={model.markers}
        hotMarker={hot}
        onMarker={onMarker}
        drops={drops}
        onAcceptDeviation={actions.acceptDeviation}
        acceptPending={pending === 'accept'}
        acceptError={pending === 'accept' ? '' : actionError}
        onOpenReview={actions.openReview}
        live={live}
        pendingCommand={model.composer.kind === 'reply' ? model.composer.pending?.command : undefined}
      />
    )
  } else if (note.parsed) {
    document = (
      <NoteDocument
        note={note.parsed}
        detail={detail}
        notePath={notePath ? noteName(notePath, notesDir) : undefined}
        markers={model.markers}
        hotMarker={hot}
        onMarker={onMarker}
        steps={model.steps}
        onGoToStep={goToStep}
        writtenAt={writtenAt}
        afterSteer={afterSteer}
        title={title}
        replyActions={replyActions}
        contact={contact}
      />
    )
  } else if (model.answer) {
    // The answer arrived but the note has not been filed (or could not be read): the answer is the document.
    document = (
      <article className="sn-note">
        {note.error ? <p className="sn-empty">{note.error}</p> : null}
        <AnswerCard
          answer={model.answer}
          markers={model.markers}
          hotMarker={hot}
          onMarker={onMarker}
          steps={model.steps}
          onGoToStep={goToStep}
          replyActions={replyActions}
        />
      </article>
    )
  } else {
    const turns = detail.usage?.turns ?? 0
    document = (
      <article className="sn-note" data-testid="document-pending">
        <div className="sn-meta" data-cols="3">
          <MetaCell k="Tracker" v={detail.key} mono />
          {detail.helpdeskKey ? <MetaCell k="Helpdesk" v={detail.helpdeskKey} mono /> : null}
          <MetaCell k="Run" v={`${detail.runId} · ${detail.provider}`} mono />
          <MetaCell k="Turns" v={`${turns} of ${detail.budget.maxTurns}`} mono />
          <MetaCell k="Cost" v={`${costOrUnknown(detail.usage?.costUsd, live)} of $${detail.budget.maxUsd}`} mono />
        </div>
        <p className="sn-note__pending">
          {note.text === null && !live
            ? 'Reading the note…'
            : note.error
              ? note.error
              : live || detail.status === 'blocked'
                ? 'The note arrives when the agent finishes. The path on the left shows what it has done so far.'
                : 'This run wrote no note.'}
        </p>
      </article>
    )
  }

  return (
    <div className="sn" data-layout="document" data-testid="session-document">
      {actionError && pending !== 'accept' && model.composer.kind === 'disabled' ? (
        <p className="sn-failed-line" role="alert">
          {actionError}
        </p>
      ) : null}
      <div className="sn__win">
        <aside className="sn-path" aria-label="The path the agent took">
          <div className="sn-path__head">
            <span className="sn-label">Path</span>
            <span className="sn-path__n">
              {model.counts.calls} {model.counts.calls === 1 ? 'call' : 'calls'}
            </span>
            <label className="sn-path__toggle">
              <Toggle checked={everything} onChange={setEverything} label="Show everything" />
              <span aria-hidden="true">everything</span>
            </label>
            <div className="sn-path__drawers">
              {!isFix ? (
                <button type="button" className="sn-dbtn" aria-expanded={drawer === 'bundle'} onClick={() => setDrawer(drawer === 'bundle' ? null : 'bundle')}>
                  <BundleIcon />
                  Bundle
                  {bundle.parsed ? <span className="sn-dbtn__n">{bundle.parsed.thread.length}</span> : null}
                </button>
              ) : null}
              <button type="button" className="sn-dbtn" aria-expanded={drawer === 'tools'} onClick={() => setDrawer(drawer === 'tools' ? null : 'tools')}>
                <ToolsIcon />
                Tools
                <span className="sn-dbtn__n">{model.counts.calls}</span>
              </button>
            </div>
          </div>
          <div className="sn-path__scroll sn-scroll" ref={pathRef} role="log" aria-label="Path" aria-live="polite">
            {model.path.length === 0 ? (
              <p className="sn-path__empty">{live ? 'No calls yet. They appear here as the agent works.' : 'The log holds no calls.'}</p>
            ) : (
              <PathList
                path={model.path}
                markers={model.markers}
                hotMarker={hot}
                open={open}
                onToggle={toggleStep}
                onMarker={onMarker}
                onOpenInTools={openInTools}
                hotRefs={hotSteps.length > 0 ? hotRefs : undefined}
                live={live}
              />
            )}
          </div>
        </aside>
        <Drawer
          open={drawer === 'bundle'}
          title="Bundle"
          meta={bundle.parsed ? `${bundle.parsed.thread.length} messages` : undefined}
          onClose={() => setDrawer(null)}
        >
          <BundlePane
            transport={transport}
            workspaceId={workspaceId}
            runId={detail.runId}
            assignee={detail.assignee}
            helpdeskKey={detail.helpdeskKey}
            onOpenFolder={openBundleFolder}
            folderPath={detail.bundleDir}
          />
        </Drawer>
        <Drawer
          open={drawer === 'tools'}
          title="Tools"
          meta={model.counts.denied > 0 ? `${model.counts.denied} denied` : undefined}
          onClose={() => setDrawer(null)}
        >
          <ToolsPane rows={toolRows} markers={model.markers} hotMarker={hot} onMarker={onMarker} highlighted={selectedStep} onRowClick={goToStep} />
        </Drawer>
        <main className="sn-doc">
          <div className="sn-doc__scroll sn-scroll" ref={docRef}>
            {document}
          </div>
          <ComposerStrip
            state={composerState}
            detail={named}
            busy={sendBusy}
            error={composerError}
            onSend={(text, decision) => (model.composer.kind === 'reply' ? actions.answer(text, decision) : actions.steer(text))}
            sentCount={sent}
            playbooks={playbooks}
            lastSteer={model.lastSteer}
            onStop={actions.cancel}
            canStop={canCancel && pending === ''}
            stopBusy={pending === 'cancel'}
          />
        </main>
      </div>
      <span className="visually-hidden" aria-live="polite">
        {hot && hotSteps.length > 0 ? `Marker ${hot}: ${markersForStep(hotSteps[0], model.markers).length} on step` : ''}
        {hot && parseRefs('').length === 0 ? '' : ''}
      </span>
    </div>
  )
}
