import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'

/*
 * Node 26 defines its own `localStorage` global that stays undefined unless the
 * process is started with `--localstorage-file`, and it shadows the one jsdom
 * would otherwise provide. The app only ever reads and writes a single string
 * (the last workspace it showed), so give the tests an in-memory Storage and
 * keep the real behaviour under test rather than the environment's quirk.
 */
if (!globalThis.localStorage) {
  const entries = new Map<string, string>()
  const storage: Storage = {
    get length() {
      return entries.size
    },
    key: (i) => [...entries.keys()][i] ?? null,
    getItem: (k) => entries.get(k) ?? null,
    setItem: (k, v) => void entries.set(k, String(v)),
    removeItem: (k) => void entries.delete(k),
    clear: () => entries.clear(),
  }
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    writable: true,
    value: storage,
  })
}

afterEach(() => {
  cleanup()
})
