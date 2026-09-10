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

/** Subscribes the calling component to the whole store snapshot. */
export function useAppState(): AppState {
  const store = useStore()
  return useSyncExternalStore(store.subscribe, store.getState, store.getState)
}
