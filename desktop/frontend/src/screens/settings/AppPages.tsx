import { useId, useSyncExternalStore } from 'react'
import { setShowLibrary, showLibrary, subscribeShowLibrary } from '../../lib/library'
import { prefersRTL, setPreferRTL, subscribePreferRTL } from '../../lib/rtl'
import SettingRow, { SettingCard } from '../../ui/setting-row'
import Toggle from '../../ui/toggle'

/**
 * The configuration reference, on GitHub. A relative `docs/config.md` resolves
 * against the asset server the bundle is loaded from, which serves the app's
 * own index.html for it — so the link led back to Sirdar rather than to the
 * documentation.
 */
export const CONFIG_DOCS_URL =
  'https://github.com/srivathsanvenkateswaran/Sirdar/blob/main/docs/config.md'

export const RTL_LABEL = 'Prefer right-to-left layout for Arabic content'

/*
 * The "This app" pages. These are the preferences of this person and this
 * browser rather than of the workspace, and each switch applies as it is
 * flipped: there is nothing here for Save to do.
 */

export function ReadingPage(): JSX.Element {
  const rtl = useSyncExternalStore(subscribePreferRTL, prefersRTL, () => false)
  return (
    <SettingCard heading="Note pane">
      <SettingRow
        label="Reading direction"
        value={rtl ? 'Right to left' : 'Each block from its own first letter'}
        help="Notes mix an English body with the customer's own Arabic, and each block is laid out from its own first letter either way. This lays the whole note pane out right to left. It is remembered in this browser and changes nothing in the workspace or in the note on disk; the run's event log stays left to right, where paths and tool names are readable."
        control={<Toggle label={RTL_LABEL} checked={rtl} onChange={setPreferRTL} />}
      />
    </SettingCard>
  )
}

export function LibraryPage(): JSX.Element {
  const library = useSyncExternalStore(subscribeShowLibrary, showLibrary, () => false)
  const labelId = useId()
  return (
    <SettingCard heading="Design library">
      <SettingRow
        label={<span id={labelId}>Show the design library</span>}
        value={library ? 'On' : 'Off'}
        help={
          <>
            Adds a Library row and the <code>#/library</code> address, where every interface
            component is shown in each of its states, in both themes and in both reading
            directions. It is for whoever is building the interface; it changes nothing about a
            run. On by default in a development build.
          </>
        }
        control={
          <Toggle
            label="Show the design library"
            labelledBy={labelId}
            checked={library}
            onChange={setShowLibrary}
          />
        }
      />
    </SettingCard>
  )
}

export function AboutPage({ version }: { version: string | null }): JSX.Element {
  return (
    <SettingCard heading="About">
      <SettingRow
        label="Sirdar"
        value={version ? `v${version}` : 'Version unknown: served by sirdar serve'}
        help={
          version
            ? 'Reported by the desktop bridge.'
            : 'The desktop app reports its build; the browser build has none to report.'
        }
      />
      <SettingRow
        label="Configuration reference"
        value={
          <a href={CONFIG_DOCS_URL} target="_blank" rel="noreferrer noopener">
            docs/config.md
          </a>
        }
      />
      <SettingRow
        label="Credentials"
        value="Never stored by Sirdar"
        help="Sirdar runs your own installed agent CLI with your login."
      />
    </SettingCard>
  )
}
