import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { ModelList, ModelSource, Transport } from '../api/types'
import { reasonOf, relativeTime, parseTime } from './format'
import { CLI_DEFAULT, modelsFor, NOT_VERIFIED, type ModelChoice } from './models'

/**
 * The model picker's list, discovered rather than typed in.
 *
 * The service answers per provider with the models the CLI resolved its
 * aliases to on this login (the probe, claude only), every model the
 * workspace's runs reported, and the operator's pins. The picker is a
 * library component and fetches nothing; a screen holds one of these and
 * hands it down. Opening the picker loads the provider's list, and only
 * when that list says a probe is due — none was taken, or the last is a day
 * old — does it ask for one. Nothing here runs on app start.
 */

/** Where one provider's list stands. */
export type CatalogEntry =
  | { state: 'loading'; list?: ModelList }
  | { state: 'ready'; list: ModelList; probing?: boolean }
  | { state: 'error'; message: string; list?: ModelList }

export interface ModelCatalog {
  /** The provider's entry, or undefined before anything asked for it. */
  entry(provider: string): CatalogEntry | undefined
  /** Reads the provider's list; probes too when the list says one is due. Called when a picker opens. */
  load(provider: string): void
  /** Probes again whatever the cache says. The picker's Refresh. */
  refresh(provider: string): void
}

/** A row's group in the list, in the order the groups are drawn. */
export type RowGroup = 'default' | ModelSource | 'fallback'

export const GROUP_HEADINGS: Record<RowGroup, string> = {
  default: 'Models',
  probe: 'On this login',
  run: 'Seen in runs',
  config: 'Pinned in config',
  fallback: 'Known names',
}

export const GROUP_ORDER: RowGroup[] = ['default', 'probe', 'run', 'config', 'fallback']

/** One model the list offers, with the group it sits under. */
export interface CatalogChoice extends ModelChoice {
  group: RowGroup
}

/** "probed today", "seen in a run 2d ago", "pinned in config". */
export function sourceNote(source: ModelSource, seenAt: string | undefined, now = Date.now()): string {
  if (source === 'config') return 'pinned in config'
  const at = parseTime(seenAt)
  if (source === 'probe') {
    if (Number.isNaN(at)) return 'probed'
    return now - at < 24 * 60 * 60 * 1000 ? 'probed today' : `probed ${relativeTime(seenAt, now)}`
  }
  const when = relativeTime(seenAt, now)
  return when ? `seen in a run ${when}` : 'seen in a run'
}

/**
 * The rows for one provider, CLI default first. A list that discovered
 * something gives its models under their sources; one that discovered
 * nothing — or has not answered yet — falls back to the static table, each
 * of its names marked as not verified on this login.
 */
export function catalogChoices(provider: string, entry: CatalogEntry | undefined, now = Date.now()): CatalogChoice[] {
  const out: CatalogChoice[] = [{ ...CLI_DEFAULT, group: 'default' }]
  const list = entry?.list
  if (list && list.models.length > 0) {
    for (const group of ['probe', 'run', 'config'] as const) {
      for (const m of list.models) {
        if (m.source !== group || !m.id) continue
        out.push({ id: m.id, label: m.label, note: sourceNote(m.source, m.seenAt, now), group })
      }
    }
    return out
  }
  for (const m of modelsFor(provider)) {
    if (m.id === '') continue
    out.push({ id: m.id, label: m.label, note: NOT_VERIFIED, group: 'fallback' })
  }
  return out
}

/**
 * One catalog per screen. The fetches are the transport's; what comes back
 * is kept per provider for as long as the screen is open, and a provider
 * whose list says a probe is due is probed once per screen at most, so a
 * picker that keeps reopening on a login whose CLI cannot answer does not
 * start the CLI each time.
 */
export function useModelCatalog(transport: Transport, workspaceId: string): ModelCatalog {
  const [entries, setEntries] = useState<Record<string, CatalogEntry>>({})
  /** Mirrors `entries` for the callbacks, which must not change on every answer. */
  const current = useRef(entries)
  current.current = entries
  const inFlight = useRef(new Set<string>())
  const autoProbed = useRef(new Set<string>())
  /** A workspace switch drops the answers for the old one. */
  const wsRef = useRef(workspaceId)

  useEffect(() => {
    wsRef.current = workspaceId
    inFlight.current.clear()
    autoProbed.current.clear()
    setEntries({})
  }, [workspaceId])

  const put = useCallback((ws: string, provider: string, next: CatalogEntry) => {
    if (wsRef.current !== ws) return
    setEntries((was) => ({ ...was, [provider]: next }))
  }, [])

  const probe = useCallback(
    (ws: string, provider: string, list?: ModelList) => {
      inFlight.current.add(provider)
      if (list) put(ws, provider, { state: 'ready', list, probing: true })
      else put(ws, provider, { state: 'loading', list: current.current[provider]?.list })
      transport
        .refreshModels(ws, provider)
        .then((next) => put(ws, provider, { state: 'ready', list: next }))
        .catch((err: unknown) => put(ws, provider, { state: 'error', message: reasonOf(err), list }))
        .finally(() => inFlight.current.delete(provider))
    },
    [transport, put],
  )

  const load = useCallback(
    (provider: string) => {
      const ws = workspaceId
      if (!ws || !provider || inFlight.current.has(provider)) return
      inFlight.current.add(provider)
      const had = current.current[provider]?.list
      put(ws, provider, { state: 'loading', list: had })
      transport
        .models(ws, provider)
        .then((list) => {
          inFlight.current.delete(provider)
          if (list.canProbe && list.probeDue && !autoProbed.current.has(provider)) {
            autoProbed.current.add(provider)
            probe(ws, provider, list)
            return
          }
          put(ws, provider, { state: 'ready', list })
        })
        .catch((err: unknown) => {
          inFlight.current.delete(provider)
          put(ws, provider, { state: 'error', message: reasonOf(err), list: had })
        })
    },
    [transport, workspaceId, put, probe],
  )

  const refresh = useCallback(
    (provider: string) => {
      if (!workspaceId || !provider || inFlight.current.has(provider)) return
      probe(workspaceId, provider, current.current[provider]?.list)
    },
    [workspaceId, probe],
  )

  return useMemo(
    () => ({ entry: (provider: string) => entries[provider], load, refresh }),
    [entries, load, refresh],
  )
}
