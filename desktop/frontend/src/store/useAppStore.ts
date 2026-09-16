import { createContext, useContext, useSyncExternalStore } from 'react'
import type { AppState, AppStore } from './appStore'

const StoreContext = createContext<AppStore | null>(null)

export const StoreProvider = StoreContext.Provider

/** The store instance for this window. Throws outside the provider. */
export function useStore(): AppStore {
  const store = useContext(StoreContext)
  if (!store) throw new Error('useStore must be used inside StoreProvider')
  return store
}

const whole = (state: AppState): AppState => state

/**
 * Subscribes the calling component to one slice of the store.
 *
 * The store emits once per change, whatever changed, and a component that
 * read the whole snapshot rendered on every one of them: a toast landing
 * re-drew the board. With a selector the component renders only when what
 * it selected is a new value, which is why the screens and the sidebar each
 * read their own slices rather than the state.
 *
 * The selector runs on every emit and must be cheap and pure, and it must
 * return something stable — a field of the state, or a value derived from
 * one and compared by identity — since a selector that built a fresh object
 * would re-render on every emit again. Reading two fields is two calls.
 * Without a selector the whole state is returned, as before.
 */
export function useAppState(): AppState
export function useAppState<T>(selector: (state: AppState) => T): T
export function useAppState<T>(selector: (state: AppState) => T = whole as never): T {
  const store = useStore()
  const read = () => selector(store.getState())
  return useSyncExternalStore(store.subscribe, read, read)
}
