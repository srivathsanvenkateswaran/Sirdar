import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import { createTransport } from './api/transport'
import { applyTheme } from './lib/theme'
import { createAppStore } from './store/appStore'
import { StoreProvider } from './store/useAppStore'
import './styles.css'

// A remembered theme is stamped before the first paint, so the window does
// not flash the system's theme on its way to the chosen one.
applyTheme()

const store = createAppStore(createTransport())

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <StoreProvider value={store}>
      <App />
    </StoreProvider>
  </React.StrictMode>,
)
