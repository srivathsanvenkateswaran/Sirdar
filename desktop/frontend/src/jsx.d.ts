import type { JSX as ReactJSX } from 'react'

/**
 * @types/react 19 dropped the global `JSX` namespace in favour of `React.JSX`.
 * The screen signatures in the desktop design spec are written as
 * `: JSX.Element`, so alias the one member those signatures use. Only `Element`
 * is re-exported: the `react-jsx` transform resolves intrinsics through
 * `react/jsx-runtime`, and redeclaring them here would shadow it.
 */
declare global {
  namespace JSX {
    type Element = ReactJSX.Element
  }
}

export {}
