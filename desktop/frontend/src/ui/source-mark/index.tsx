import type { CSSProperties } from 'react'
import './SourceMark.css'

export type SourceMarkSize = 'xs' | 'sm' | 'md' | 'lg'

export interface SourceMarkProps {
  /** The source as the config names it: `jira`, `zohodesk`, `exec`, … */
  adapter: string
  /**
   * The source's display name, from the config summary: the product's name,
   * or for an exec adapter the name of the tool behind it ("Janus"). It is
   * the accessible name, and the initials tile is cut from it.
   */
  name?: string
  /** 16, 22, 28 or 40px tile. */
  size?: SourceMarkSize
  /** Overrides the accessible name. */
  label?: string
}

/**
 * The trackers' and helpdesks' own marks, as distributed by Simple Icons
 * (cdn.simpleicons.org), and the brand colour each ships behind its inverted
 * mark. Fetched 2026-09-16; six of the thirteen adapters have no mark there
 * (Azure DevOps, Rally, ServiceNow, Freshdesk, Front, Gorgias) and draw their
 * initials instead, as an exec adapter does.
 *
 * They are trademarks, used only to say which product a ticket number belongs
 * to. If a vendor's brand guidelines ever object, drop its entry here and the
 * tile falls back to the initials.
 *
 * The brand colours live in this file and nowhere else, as ProviderMark's do:
 * `styles/tokens.css` has no vendor in it, and `src/ui/library.test.ts`
 * holds every stylesheet to that.
 */
interface Mark {
  /** The brand colour behind a white mark. */
  tile: string
  /** SVG path data on a 24-unit box. */
  path: string
}

const MARKS: Record<string, Mark> = {
  jira: {
    tile: '#0052CC',
    path: 'M11.571 11.513H0a5.218 5.218 0 0 0 5.232 5.215h2.13v2.057A5.215 5.215 0 0 0 12.575 24V12.518a1.005 1.005 0 0 0-1.005-1.005zm5.723-5.756H5.736a5.215 5.215 0 0 0 5.215 5.214h2.129v2.058a5.218 5.218 0 0 0 5.215 5.214V6.758a1.001 1.001 0 0 0-1.001-1.001zM23.013 0H11.455a5.215 5.215 0 0 0 5.215 5.215h2.129v2.057A5.215 5.215 0 0 0 24 12.483V1.005A1.001 1.001 0 0 0 23.013 0Z',
  },
  linear: {
    tile: '#5E6AD2',
    path: 'M2.886 4.18A11.982 11.982 0 0 1 11.99 0C18.624 0 24 5.376 24 12.009c0 3.64-1.62 6.903-4.18 9.105L2.887 4.18ZM1.817 5.626l16.556 16.556c-.524.33-1.075.62-1.65.866L.951 7.277c.247-.575.537-1.126.866-1.65ZM.322 9.163l14.515 14.515c-.71.172-1.443.282-2.195.322L0 11.358a12 12 0 0 1 .322-2.195Zm-.17 4.862 9.823 9.824a12.02 12.02 0 0 1-9.824-9.824Z',
  },
  zohodesk: {
    tile: '#E42527',
    path: 'M8.66 6.897a1.299 1.299 0 0 0-1.205.765l-.642 1.44-.062-.385A1.291 1.291 0 0 0 5.27 7.648l-4.185.678A1.291 1.291 0 0 0 .016 9.807l.678 4.18a1.293 1.293 0 0 0 1.27 1.087c.074 0 .143-.01.216-.017l4.18-.678c.436-.07.784-.351.96-.723l2.933 1.307a1.304 1.304 0 0 0 .988.026c.321-.12.575-.365.716-.678l.28-.629.038.276a1.297 1.297 0 0 0 1.455 1.103l3.712-.501a1.29 1.29 0 0 0 1.03.514h4.236c.713 0 1.29-.58 1.291-1.291V9.545c0-.712-.58-1.291-1.291-1.291h-4.236c-.079 0-.155.008-.23.022a1.309 1.309 0 0 0-.275-.288c-.275-.21-.614-.3-.958-.253l-4.197.571c-.155.021-.3.07-.432.14L9.159 7.01a1.27 1.27 0 0 0-.499-.113zm-.025.705c.077 0 .159.013.24.052l2.971 1.324c-.128.238-.18.508-.142.782l.357 2.596h.002l-.745 1.672a.59.59 0 0 1-.777.296l-3.107-1.385-.004-.041-.41-2.526L8.1 7.95a.589.589 0 0 1 .536-.348zm-3.159.733c.125 0 .245.039.343.112.13.09.21.227.237.382l.234 1.446-.56 1.259a1.27 1.27 0 0 0-.026.987c.12.322.364.575.678.717l.295.131a.585.585 0 0 1-.428.314l-4.185.678a.59.59 0 0 1-.674-.485l-.678-4.18a.588.588 0 0 1 .485-.674l4.185-.678c.03-.004.064-.01.094-.01zm11.705.09a.59.59 0 0 1 .415.173 1.287 1.287 0 0 0-.416.947v4.237c0 .033.003.065.005.097l-3.55.482a.586.586 0 0 1-.66-.502l-.191-1.403.899-2.017a1.29 1.29 0 0 0-.333-1.5l3.754-.51c.026-.004.051-.004.077-.004zm1.3.532h4.227c.326 0 .588.266.588.588v4.237a.589.589 0 0 1-.588.588h-4.237a.564.564 0 0 1-.12-.013c.47-.246.758-.765.684-1.318zm-5.988.309.254.113c.296.133.43.48.296.777l-.432.97-.207-1.465a.58.58 0 0 1 .09-.395zm5.39.538.453 3.325a.583.583 0 0 1-.453.65zM6.496 11.545l.17 1.052a.588.588 0 0 1-.293-.776zm3.985 4.344a.588.588 0 0 0-.612.603c0 .358.244.61.601.61a.582.582 0 0 0 .607-.608c0-.35-.242-.605-.596-.605zm5.545 0a.588.588 0 0 0-.612.603c0 .358.245.61.602.61a.582.582 0 0 0 .606-.608c0-.35-.24-.605-.596-.605zm-8.537.018a.047.047 0 0 0-.048.047v.085c0 .026.021.047.048.047h.52l-.623.9a.052.052 0 0 0-.009.027v.027c0 .026.021.047.048.047h.815a.047.047 0 0 0 .047-.047v-.085a.047.047 0 0 0-.047-.047h-.55l.606-.9a.05.05 0 0 0 .008-.026v-.028a.047.047 0 0 0-.047-.047zm5.303 0a.047.047 0 0 0-.047.047v1.086c0 .026.02.047.047.047h.135a.047.047 0 0 0 .047-.047v-.454h.545v.454c0 .026.02.047.047.047h.134a.047.047 0 0 0 .047-.047v-1.086a.047.047 0 0 0-.047-.047h-.134a.047.047 0 0 0-.047.047v.453h-.545v-.453a.047.047 0 0 0-.047-.047zm-2.324.164c.25 0 .372.194.372.425 0 .219-.109.425-.358.426-.242 0-.375-.197-.375-.419 0-.235.108-.432.36-.432zm5.545 0c.25 0 .372.194.372.425 0 .219-.108.425-.358.426-.242 0-.374-.197-.374-.419 0-.235.108-.432.36-.432z',
  },
  zendesk: {
    tile: '#03363D',
    path: 'M12.914 2.904V16.29L24 2.905H12.914zM0 2.906C0 5.966 2.483 8.45 5.543 8.45s5.542-2.484 5.543-5.544H0zm11.086 4.807L0 21.096h11.086V7.713zm7.37 7.84c-3.063 0-5.542 2.48-5.542 5.543H24c0-3.06-2.48-5.543-5.543-5.543z',
  },
  helpscout: {
    tile: '#1292EE',
    path: 'm3.497 14.044 7.022-7.021a4.946 4.946 0 0 0 1.474-3.526A4.99 4.99 0 0 0 10.563 0L3.54 7.024a4.945 4.945 0 0 0-1.473 3.525c0 1.373.55 2.6 1.43 3.496zm17.007-4.103-7.023 7.022a4.946 4.946 0 0 0-1.473 3.525c0 1.36.55 2.601 1.43 3.497l7.022-7.022a4.943 4.943 0 0 0 1.474-3.526c0-1.373-.55-2.6-1.43-3.496zm-.044-2.904a4.944 4.944 0 0 0 1.474-3.525c0-1.36-.55-2.6-1.43-3.497L3.54 16.965A4.986 4.986 0 0 0 3.497 24Z',
  },
  intercom: {
    // Simple Icons files Intercom under #6AFDEF, a cyan a white mark is
    // lost on. The tile takes the blue of Intercom's own app icon instead.
    tile: '#286EFA',
    path: 'M21 0H3C1.343 0 0 1.343 0 3v18c0 1.658 1.343 3 3 3h18c1.658 0 3-1.342 3-3V3c0-1.657-1.342-3-3-3zm-5.801 4.399c0-.44.36-.8.802-.8.44 0 .8.36.8.8v10.688c0 .442-.36.801-.8.801-.443 0-.802-.359-.802-.801V4.399zM11.2 3.994c0-.44.357-.799.8-.799s.8.359.8.799v11.602c0 .44-.357.8-.8.8s-.8-.36-.8-.8V3.994zm-4 .405c0-.44.359-.8.799-.8.443 0 .802.36.802.8v10.688c0 .442-.36.801-.802.801-.44 0-.799-.359-.799-.801V4.399zM3.199 6c0-.442.36-.8.802-.8.44 0 .799.358.799.8v7.195c0 .441-.359.8-.799.8-.443 0-.802-.36-.802-.8V6zM20.52 18.202c-.123.105-3.086 2.593-8.52 2.593-5.433 0-8.397-2.486-8.521-2.593-.335-.288-.375-.792-.086-1.128.285-.334.79-.375 1.125-.09.047.041 2.693 2.211 7.481 2.211 4.848 0 7.456-2.186 7.479-2.207.334-.289.839-.25 1.128.086.289.336.25.84-.086 1.128zm.281-5.007c0 .441-.36.8-.801.8-.441 0-.801-.36-.801-.8V6c0-.442.361-.8.801-.8.441 0 .801.357.801.8v7.195z',
  },
  hubspot: {
    tile: '#FF7A59',
    path: 'M18.164 7.93V5.084a2.198 2.198 0 001.267-1.978v-.067A2.2 2.2 0 0017.238.845h-.067a2.2 2.2 0 00-2.193 2.193v.067a2.196 2.196 0 001.252 1.973l.013.006v2.852a6.22 6.22 0 00-2.969 1.31l.012-.01-7.828-6.095A2.497 2.497 0 104.3 4.656l-.012.006 7.697 5.991a6.176 6.176 0 00-1.038 3.446c0 1.343.425 2.588 1.147 3.607l-.013-.02-2.342 2.343a1.968 1.968 0 00-.58-.095h-.002a2.033 2.033 0 102.033 2.033 1.978 1.978 0 00-.1-.595l.005.014 2.317-2.317a6.247 6.247 0 104.782-11.134l-.036-.005zm-.964 9.378a3.206 3.206 0 113.215-3.207v.002a3.206 3.206 0 01-3.207 3.207z',
  },
}

/**
 * Each built-in adapter's product name, the same table the service's config
 * summary uses. It is the name a tile is cut from when the caller gives none.
 */
export const SOURCE_NAMES: Record<string, string> = {
  jira: 'Jira',
  linear: 'Linear',
  azdo: 'Azure DevOps',
  rally: 'Rally',
  servicenow: 'ServiceNow',
  zohodesk: 'Zoho Desk',
  zendesk: 'Zendesk',
  freshdesk: 'Freshdesk',
  helpscout: 'Help Scout',
  intercom: 'Intercom',
  hubspot: 'HubSpot',
  front: 'Front',
  gorgias: 'Gorgias',
}

/** The adapters this component has a mark for. */
export const MARKED_SOURCES = Object.keys(MARKS) as readonly string[]

/** The product's name for an adapter, or the adapter itself when none is known. */
export function sourceName(adapter: string, name?: string): string {
  return name?.trim() || SOURCE_NAMES[adapter] || adapter
}

/**
 * Two letters for a name: the initials of its first two words ("Azure
 * DevOps" → AD, "ServiceNow" → SN, read at the capital), else the first two
 * letters ("Janus" → JA). Upper-cased, since the tile is a mark.
 */
export function sourceInitials(name: string): string {
  const words = name
    .trim()
    .replace(/([a-z])([A-Z])/g, '$1 $2')
    .split(/[\s_-]+/)
    .filter(Boolean)
  if (words.length === 0) return '?'
  if (words.length >= 2) return (words[0][0] + words[1][0]).toUpperCase()
  return words[0].slice(0, 2).toUpperCase()
}

/**
 * Which tracker or helpdesk a ticket number belongs to, as a tile.
 *
 * The product's own mark in white on its brand colour, the way ProviderMark
 * draws a vendor; a source with no public mark — six of the built-in
 * adapters, and every `exec` adapter such as a private tracker — gets two
 * letters of its display name on the ink, so a row never has a hole where the
 * source should be. It says which source, and nothing else.
 *
 * The accessible name is the source's display name, so a screen reader reads
 * "Zoho Desk" where a sighted reader sees the mark.
 */
export default function SourceMark({
  adapter,
  name,
  size = 'md',
  label,
}: SourceMarkProps): JSX.Element {
  const mark = MARKS[adapter]
  const shown = sourceName(adapter, name)
  const style = mark ? ({ '--sd-source-tile': mark.tile } as CSSProperties) : undefined
  return (
    <span
      className="sd-source-mark"
      data-size={size}
      data-adapter={adapter}
      data-branded={mark ? 'true' : undefined}
      role="img"
      aria-label={label ?? shown}
      title={label ?? shown}
      style={style}
    >
      {mark ? (
        <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" focusable="false">
          <path d={mark.path} />
        </svg>
      ) : (
        <span className="sd-source-mark__initials" aria-hidden="true">
          {sourceInitials(shown)}
        </span>
      )}
    </span>
  )
}
