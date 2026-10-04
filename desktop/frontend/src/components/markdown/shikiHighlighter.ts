/**
 * Shiki, loaded the first time a fenced code block mounts.
 *
 * Shiki's grammars are large enough that importing the package at the top
 * of `ChatMarkdown.tsx` would put them in every screen's entry chunk, most
 * of which never render a line of code. The dynamic `import('shiki')`
 * defers that cost to the moment a code block actually needs it, and every
 * block after the first shares one highlighter and whatever themes and
 * languages it has already loaded.
 */

type ShikiModule = typeof import('shiki')
type Highlighter = Awaited<ReturnType<ShikiModule['getSingletonHighlighter']>>

const THEMES = ['github-light', 'github-dark'] as const
export type ChatMarkdownTheme = (typeof THEMES)[number]

let highlighterPromise: Promise<Highlighter> | null = null

/** The one highlighter every code block shares, created on first use. */
function sharedHighlighter(): Promise<Highlighter> {
  if (!highlighterPromise) {
    highlighterPromise = import('shiki').then((shiki) => shiki.getSingletonHighlighter({ themes: [...THEMES], langs: [] }))
  }
  return highlighterPromise
}

/**
 * Light or dark, from the attribute `src/lib/theme.ts` stamps on the root
 * element, or the system's preference when "system" leaves it unstamped —
 * the same fallback `styles/tokens.css` uses for its own media query.
 */
export function chatMarkdownTheme(): ChatMarkdownTheme {
  if (typeof document === 'undefined') return 'github-light'
  const stamped = document.documentElement.dataset.theme
  if (stamped === 'dark') return 'github-dark'
  if (stamped === 'light') return 'github-light'
  const prefersDark = typeof window !== 'undefined' && window.matchMedia?.('(prefers-color-scheme: dark)').matches
  return prefersDark ? 'github-dark' : 'github-light'
}

/**
 * Highlighted HTML for `code` as `lang`, loading that language into the
 * shared highlighter on demand. Returns `null` for a language Shiki does
 * not know, so the caller can fall back to plain text.
 */
export async function highlightCode(code: string, lang: string): Promise<string | null> {
  try {
    const highlighter = await sharedHighlighter()
    if (!highlighter.getLoadedLanguages().includes(lang)) {
      await highlighter.loadLanguage(lang as Parameters<Highlighter['loadLanguage']>[0])
    }
    return highlighter.codeToHtml(code, { lang, theme: chatMarkdownTheme() })
  } catch {
    return null
  }
}
