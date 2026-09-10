import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import { createTransport } from './api/transport'
import { createAppStore } from './store/appStore'
import { StoreProvider } from './store/useAppStore'
import './styles.css'

const store = createAppStore(createTransport())

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <StoreProvider value={store}>
      <App />
    </StoreProvider>
  </React.StrictMode>,
)
