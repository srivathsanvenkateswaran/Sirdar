import { afterEach, describe, expect, it } from 'vitest'
import { createHTTPTransport, createWailsTransport } from './transport'
import { TRANSPORT_METHODS, type Transport, type TransportMethod } from './types'

/*
 * One bundle, two transports.
 *
 * The web UI `sirdar serve` hands a browser, the macOS app and the Windows
 * app are the same `desktop/frontend` build; the only difference is which
 * `Transport` it is given — `createHTTPTransport` over the JSON API and the
 * SSE stream, or `createWailsTransport` over the bound Go methods. So a
 * feature that reaches one implementation and not the other is a platform
 * that quietly lost it, and it was lost silently: the screen calls an
 * optional method, finds nothing there, and leaves the control out.
 *
 * `TRANSPORT_METHODS` in ./types is the interface's own list, kept honest by
 * a type-level check beside it. This walks that list against both
 * implementations. A method may be missing from the browser only by being
 * named in DESKTOP_ONLY below, with the reason.
 */

/**
 * The methods only the desktop shell can answer, each with why a browser
 * cannot — and what the web UI does instead. Nothing else may be missing
 * from either side.
 *
 * `attachmentURL` is deliberately not here: both transports answer it, the
 * browser with the attachment route and the desktop with a data URL from
 * `Bridge.AttachmentDataURL`, because WKWebView will not load a `file://`
 * subresource.
 */
const DESKTOP_ONLY: Record<string, string> = {
  version: 'the build version of the desktop binary; a browser is served by whatever `sirdar serve` is running',
  openConfig: 'opens config.yaml in the machine’s editor; the browser copies the path instead',
  openNote: 'opens a note in the machine’s Markdown editor; the browser copies the path instead',
  openRunDir: 'reveals the run directory in the file manager; the browser copies the path instead',
}

const http: Transport = createHTTPTransport()
const wails: Transport = createWailsTransport()

function has(transport: Transport, name: TransportMethod): boolean {
  return typeof transport[name] === 'function'
}

afterEach(() => {
  delete (window as unknown as { go?: unknown }).go
})

describe('the two transports carry the same Transport', () => {
  it('lists every name the interface declares', () => {
    // The type-level check in ./types is the real guard; this says out loud
    // that the list is not empty and holds no duplicates.
    expect(TRANSPORT_METHODS.length).toBeGreaterThan(40)
    expect(new Set(TRANSPORT_METHODS).size).toBe(TRANSPORT_METHODS.length)
  })

  it('the Wails transport implements every one of them', () => {
    const missing = TRANSPORT_METHODS.filter((name) => !has(wails, name))
    expect(missing).toEqual([])
  })

  it('the HTTP transport implements every one except the desktop-only few', () => {
    const missing = TRANSPORT_METHODS.filter((name) => !has(http, name) && !(name in DESKTOP_ONLY))
    expect(missing).toEqual([])
  })

  it('every desktop-only entry is a real Transport method the browser really lacks', () => {
    for (const [name, reason] of Object.entries(DESKTOP_ONLY)) {
      expect(TRANSPORT_METHODS).toContain(name)
      expect(reason.length).toBeGreaterThan(20)
      // A stale excuse is as bad as a missing method: it would go on
      // excusing something the browser has since grown.
      expect(has(http, name as TransportMethod)).toBe(false)
      expect(has(wails, name as TransportMethod)).toBe(true)
    }
  })

  it('picks the bridge when the shell has bound one, and the HTTP client otherwise', async () => {
    const { createTransport, hasWailsBridge } = await import('./transport')
    expect(hasWailsBridge()).toBe(false)
    expect(createTransport().openConfig).toBeUndefined()
    ;(window as unknown as { go: unknown }).go = { main: { Bridge: {} } }
    expect(hasWailsBridge()).toBe(true)
    expect(typeof createTransport().openConfig).toBe('function')
  })
})
