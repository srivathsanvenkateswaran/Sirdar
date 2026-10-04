import type { Element, ElementContent } from 'hast'
import type { JSX } from 'react'
import ReactMarkdown, { type Components } from 'react-markdown'
import rehypeSanitize from 'rehype-sanitize'
import remarkBreaks from 'remark-breaks'
import remarkGfm from 'remark-gfm'
import CodeBlock from './CodeBlock'
import TableWrapper from './TableWrapper'
import './chat-markdown.css'

// Re-exported so a caller building a `components` override — the `path:line`
// button in `Prose.tsx`, say — never needs its own `import ... from
// 'react-markdown'`; this file is the only one that does.
export type { Components } from 'react-markdown'

/** Every text node under a hast element, concatenated — the raw code Shiki highlights. */
function textOf(node: ElementContent): string {
  if (node.type === 'text') return node.value
  if ('children' in node) return node.children.map(textOf).join('')
  return ''
}

/** The `language-x` class fenced code gets from remark, without the prefix. */
function languageOf(code: Element): string | undefined {
  const className = code.properties?.className
  const classes = Array.isArray(className) ? className : typeof className === 'string' ? [className] : []
  const match = classes.map(String).find((name) => name.startsWith('language-'))
  return match?.slice('language-'.length)
}

function isElement(node: ElementContent): node is Element {
  return node.type === 'element'
}

/**
 * The defaults every chat surface renders Markdown with: fenced code as a
 * `CodeBlock`, GFM tables in a scrolling, copyable strip, and links that
 * leave the app rather than replace it. A caller's `components` is spread
 * over this, so a key it supplies — `code`, to turn a `path:line` span into
 * a button — replaces the matching default rather than losing to it.
 */
const defaultComponents: Components = {
  pre: ({ node, children }) => {
    const code = node?.children.find(isElement)
    // A fence with no language still gets `CodeBlock` — it already draws the
    // plain, unhighlighted block its missing language leaves it with — so
    // every `pre > code` on a chat surface gets the same frame and Copy
    // button, and none of them fall through to `.cmd code`'s single-line
    // inline style the way a bare `<pre>{children}</pre>` would have.
    if (code?.tagName !== 'code') return <pre>{children}</pre>
    return <CodeBlock code={textOf(code).replace(/\n$/, '')} language={languageOf(code)} />
  },
  table: ({ children }) => <TableWrapper>{children}</TableWrapper>,
  a: ({ children, ...props }) => (
    <a {...props} target="_blank" rel="noreferrer">
      {children}
    </a>
  ),
}

export interface ChatMarkdownProps {
  /** The Markdown source. */
  children: string
  /** Spread over the defaults; a key here replaces that default's component. */
  components?: Partial<Components>
  /** Appended to the wrapper's own `cmd` class. */
  className?: string
}

/**
 * The one Markdown renderer every chat surface uses: `react-markdown` with
 * GFM (tables, task lists, strikethrough), single newlines as line breaks,
 * and the output run through `rehype-sanitize`'s default schema, so a
 * `<script>` tag pasted into a transcript or a note never executes.
 *
 * Replaces the bare `react-markdown` each session layout used to call on
 * its own, which rendered GFM syntax as literal asterisks and pipes and
 * had no code highlighting at all.
 */
export default function ChatMarkdown({ children, components, className }: ChatMarkdownProps): JSX.Element {
  return (
    <div className={className ? `cmd ${className}` : 'cmd'} dir="auto">
      <ReactMarkdown
        remarkPlugins={[remarkGfm, remarkBreaks]}
        rehypePlugins={[rehypeSanitize]}
        components={{ ...defaultComponents, ...components }}
      >
        {children}
      </ReactMarkdown>
    </div>
  )
}
