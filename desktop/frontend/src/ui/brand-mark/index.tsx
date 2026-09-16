import './BrandMark.css'

export type BrandMarkSize = 'sm' | 'md' | 'lg'

export interface BrandMarkProps {
  /** 16, 20 or 32px. The sidebar wordmark takes the 20. */
  size?: BrandMarkSize
  /**
   * Draws the mark as decoration beside a word that already says "Sirdar",
   * which is the sidebar's case: a second accessible name there would have a
   * screen reader read the product twice.
   */
  decorative?: boolean
  /** Overrides the accessible name. "Sirdar" otherwise. */
  label?: string
}

/*
 * The brand's four colours, in this file and nowhere else, the way
 * `provider-mark` keeps the vendors' brand colours in its TSX:
 * `styles/tokens.css` names no brand, and `src/ui/library.test.ts` holds every
 * stylesheet to that. The masters are
 * `docs/design/2026-09-16-logo/final/sirdar-mark-{light,dark}.svg`, on the same
 * 512 grid as the geometry below.
 */
const CRIMSON = '#DC143C'
const BLUE = '#003893'
const WHITE = '#FFFFFF'
const PAPER = '#FAF7EC'

/** The back peak, the front peak, the route and its summit marker, on 512. */
const BACK_PEAK = 'M152 408 L304 104 L456 408 Z'
const FRONT_PEAK = 'M64 408 L176 220 L288 408 Z'
const ROUTE = 'M116 408 C200 380 150 300 236 262 C300 234 276 200 302 160'

interface Variant {
  /** The peak behind, which is the one that carries the theme. */
  back: string
  /** The route and the summit marker. */
  line: string
}

const VARIANTS: Record<'light' | 'dark', Variant> = {
  light: { back: BLUE, line: WHITE },
  dark: { back: PAPER, line: BLUE },
}

function Peaks({ variant }: { variant: 'light' | 'dark' }): JSX.Element {
  const { back, line } = VARIANTS[variant]
  return (
    <g data-variant={variant}>
      <path d={BACK_PEAK} fill={back} />
      <path d={FRONT_PEAK} fill={CRIMSON} />
      <path d={ROUTE} fill="none" stroke={line} strokeWidth="22" strokeLinecap="round" />
      <circle cx="302" cy="160" r="19" fill={line} />
    </g>
  )
}

/**
 * Sirdar's own mark, chosen on 2026-09-16 from the six in
 * `docs/design/2026-09-16-logo/`: a twin peak with a route to the summit, in
 * the colours of Nepal's flag, since a sirdar is the head guide of a Himalayan
 * expedition.
 *
 * Bare: no tile, no ground, nothing but the drawing, so it sits on whatever
 * surface it is put on. The squircle tile is the app icon's business, not this
 * component's — the tiles are `sirdar-tile-{light,dark}.svg` in the logo folder
 * and `scripts/make-icons.sh` turns the light one into
 * `desktop/build/appicon.png`.
 *
 * Crimson holds in both themes. Blue and white trade places: the light mark is
 * a blue peak behind the crimson one with a white route, the dark mark a paper
 * peak with the route in blue. Which one is drawn is the theme's decision, not
 * a prop — `BrandMark.css` switches them on the same `data-theme` the palette
 * switches on, so a mark inside the gallery's dark specimen frame is dark
 * while the page around it stays light.
 */
export default function BrandMark({
  size = 'md',
  decorative = false,
  label,
}: BrandMarkProps): JSX.Element {
  const name = label ?? 'Sirdar'
  const titled = decorative
    ? { 'aria-hidden': true as const }
    : { role: 'img' as const, 'aria-label': name }
  return (
    <span className="sd-brand-mark" data-size={size} {...titled}>
      <svg viewBox="0 0 512 512" aria-hidden="true" focusable="false">
        <Peaks variant="light" />
        <Peaks variant="dark" />
      </svg>
    </span>
  )
}
