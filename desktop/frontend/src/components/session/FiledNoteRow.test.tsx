import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { RunSummary } from '../../api/types'
import FiledNoteRow, { SaveNoteRow } from './FiledNoteRow'

const RUN: RunSummary = {
  runId: '20261004T101500Z-ab12',
  key: 'OMNI-1',
  kind: 'triage',
  replyFirst: true,
  status: 'completed',
  provider: 'claude',
  model: 'claude-opus-5',
  startedAt: '2026-10-04T10:15:00Z',
  updatedAt: '2026-10-04T10:18:00Z',
  reason: '',
  usage: { turns: 3, inputTokens: 0, outputTokens: 0, costUsd: 0 },
  notes: ['/run/note.md', '/notes/OMNI-1 refund-stuck.md'],
}

function row(over: Partial<RunSummary> = {}, props: { updating?: boolean; error?: string } = {}) {
  const onOpenNote = vi.fn()
  const onUpdateNote = vi.fn(async () => {})
  render(<FiledNoteRow run={{ ...RUN, ...over }} onOpenNote={onOpenNote} onUpdateNote={onUpdateNote} updating={props.updating ?? false} error={props.error ?? ''} />)
  return { onOpenNote, onUpdateNote }
}

describe('FiledNoteRow', () => {
  it('draws nothing while the note is being filed', () => {
    row({ status: 'running', phase: 'note' })
    expect(screen.queryByTestId('filed-note')).toBeNull()
  })

  it('names the filed note and opens it', () => {
    const { onOpenNote, onUpdateNote } = row()
    const line = screen.getByTestId('filed-note')
    expect(line).toHaveTextContent('Filed as a note →')
    fireEvent.click(screen.getByRole('button', { name: 'OMNI-1 refund-stuck' }))
    expect(onOpenNote).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('button', { name: 'Update note' }))
    expect(onUpdateNote).toHaveBeenCalledTimes(1)
  })

  it('says why the note did not file and offers to file it again', () => {
    const { onUpdateNote } = row({ noteWarning: 'note not filed: schema validation failed twice: x' }, { error: 'conflict refused' })
    expect(screen.getByTestId('filed-note')).toHaveTextContent('Note not filed: schema validation failed twice: x')
    expect(screen.getByRole('alert')).toHaveTextContent('conflict refused')
    fireEvent.click(screen.getByRole('button', { name: 'Update note' }))
    expect(onUpdateNote).toHaveBeenCalledTimes(1)
  })

  it('draws nothing with no note and no warning', () => {
    row({ notes: [] })
    expect(screen.queryByTestId('filed-note')).toBeNull()
  })
})

describe('SaveNoteRow', () => {
  it('saves, names the file, and offers to save again', async () => {
    const onSave = vi.fn(async () => ({ path: '/notes/Sessions/ASK-20261004-why refund.md' }))
    render(<SaveNoteRow onSave={onSave} />)
    fireEvent.click(screen.getByRole('button', { name: 'Save as note' }))
    await waitFor(() => expect(screen.getByTestId('save-note')).toHaveTextContent('Saved → ASK-20261004-why refund'))
    expect(screen.getByRole('button', { name: 'Save again' })).toBeInTheDocument()
  })

  it('shows a refusal inline', async () => {
    const onSave = vi.fn(async () => {
      throw new Error('conflict: the run is still live')
    })
    render(<SaveNoteRow onSave={onSave} />)
    fireEvent.click(screen.getByRole('button', { name: 'Save as note' }))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('the run is still live'))
    expect(screen.getByRole('button', { name: 'Save as note' })).toBeInTheDocument()
  })
})
