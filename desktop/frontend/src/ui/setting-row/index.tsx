import type { ReactNode } from 'react'
import './SettingRow.css'

export interface SettingCardProps {
  /** The group's heading. A card with one row in it can go without. */
  heading?: ReactNode
  children: ReactNode
}

/**
 * A group of settings that share one heading.
 *
 * `--sd-card-row` on the modal's sheet: 1.11:1 light and 1.12:1 dark, which is
 * a step in the direction that reads as raised in either theme. No border, no
 * shadow — the colour step is the whole separation, as it is for the sheet on
 * the shell.
 */
export function SettingCard({ heading, children }: SettingCardProps): JSX.Element {
  return (
    <section className="sd-setting-card">
      {heading && <h3 className="sd-setting-card__heading">{heading}</h3>}
      <div className="sd-setting-card__rows">{children}</div>
    </section>
  )
}

export interface SettingRowProps {
  label: ReactNode
  /** What the setting is right now, under the label. */
  value?: ReactNode
  /**
   * Exactly one control, on the trailing side. A setting that needs two
   * controls is two rows, which is the rule this prop's singular name keeps.
   */
  control?: ReactNode
  /** A sentence under the value, when the setting needs one. */
  help?: ReactNode
}

/**
 * One setting.
 *
 * The label says what it is and the line under it says what it is set to, so
 * the card can be read down its leading edge without touching anything. The
 * trailing side holds one control and never two: a row with a picker and a
 * Reset beside it makes the reader work out which one the value belongs to.
 */
export default function SettingRow({ label, value, control, help }: SettingRowProps): JSX.Element {
  return (
    <div className="sd-setting-row">
      <div className="sd-setting-row__main">
        <span className="sd-setting-row__label">{label}</span>
        {value !== undefined && value !== null && value !== '' && (
          <span className="sd-setting-row__value" dir="auto">
            {value}
          </span>
        )}
        {help && <p className="sd-setting-row__help">{help}</p>}
      </div>
      {control && <div className="sd-setting-row__control">{control}</div>}
    </div>
  )
}
