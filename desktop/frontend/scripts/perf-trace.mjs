#!/usr/bin/env node
/**
 * Timing the served app in headless Chrome over the DevTools protocol.
 *
 *   node scripts/perf-trace.mjs --url http://127.0.0.1:47347 --workspace <id> --run <runId> \
 *     [--chrome "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"] [--rounds 5] [--rest 20]
 *
 * It opens the board, then measures, each several rounds, the median of:
 *   boot     navigation start → the first frame after the lanes hold a card
 *   nav      a sidebar row click → the first frame after the new screen's head is up, and back
 *   run      the address set to a run → the first frame with a row in the transcript,
 *            and the moment the transcript stops growing. `scripts/session-trace.mjs`
 *            takes the Session window itself further: scrolling, the composer, the
 *            layout switcher, and a live stream replayed into a running run
 *   hover    the pointer resting on a sessions row → the first frame with its card
 *   type     one keystroke in the board filter → the first frame after the lanes re-filter
 * and, at rest on the board for `--rest` seconds, how many React commits happened and how
 * many components each one rendered — the price of the age tick. Nothing here starts a run.
 *
 * Commits are counted through the React DevTools hook, which production React calls on
 * every commit; a component counts as rendered when its props or hook state object is a new
 * reference, the same test DevTools uses. Names are the bundle's, so run this against a
 * build with `--minify false` when the breakdown matters more than the boot number.
 *
 * Paint moments come from the page's own clock: the DOM condition is met, then two
 * animation frames pass, which is when the frame that showed it has been handed off.
 * Alongside, a Performance trace is recorded per scenario and summed: main-thread task
 * time, tasks over 50ms, and the longest task.
 */
import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { spawn } from 'node:child_process'

const args = Object.fromEntries(
  process.argv
    .slice(2)
    .join(' ')
    .split(/\s+--/)
    .filter(Boolean)
    .map((part) => {
      const [key, ...rest] = part.replace(/^--/, '').split(' ')
      return [key, rest.join(' ') || 'true']
    }),
)
const URL_BASE = (args.url ?? 'http://127.0.0.1:47347').replace(/\/$/, '')
const WORKSPACE = args.workspace
const RUN = args.run
const CHROME = args.chrome ?? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
const ROUNDS = Number(args.rounds ?? 5)
const REST_S = Number(args.rest ?? 20)
const PORT = Number(args.port ?? 9333)
if (!WORKSPACE || !RUN) {
  console.error('need --workspace <id> and --run <runId>')
  process.exit(2)
}

const median = (xs) => {
  const s = xs.slice().sort((a, b) => a - b)
  return s.length === 0 ? NaN : s.length % 2 ? s[(s.length - 1) / 2] : (s[s.length / 2 - 1] + s[s.length / 2]) / 2
}
const ms = (x) => (Number.isFinite(x) ? `${x.toFixed(1)}ms` : '—')

/* ---------- a CDP client on the page target ---------- */

class CDP {
  constructor(ws) {
    this.ws = ws
    this.seq = 0
    this.pending = new Map()
    this.handlers = new Map()
    ws.addEventListener('message', (m) => {
      const msg = JSON.parse(m.data)
      if (msg.id !== undefined) {
        const p = this.pending.get(msg.id)
        this.pending.delete(msg.id)
        if (msg.error) p.reject(new Error(`${msg.error.message} (${JSON.stringify(msg.error.data ?? '')})`))
        else p.resolve(msg.result)
        return
      }
      for (const h of this.handlers.get(msg.method) ?? []) h(msg.params)
    })
  }
  send(method, params = {}) {
    const id = ++this.seq
    if (process.env.PERF_DEBUG) console.error(`> ${method}`)
    this.ws.send(JSON.stringify({ id, method, params }))
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id)
        reject(new Error(`${method} did not answer in 60s`))
      }, 60_000)
      this.pending.set(id, {
        resolve: (v) => {
          clearTimeout(timer)
          resolve(v)
        },
        reject: (e) => {
          clearTimeout(timer)
          reject(e)
        },
      })
    })
  }
  on(method, handler) {
    if (!this.handlers.has(method)) this.handlers.set(method, new Set())
    this.handlers.get(method).add(handler)
    return () => this.handlers.get(method).delete(handler)
  }
  once(method) {
    return new Promise((resolve) => {
      const off = this.on(method, (p) => {
        off()
        resolve(p)
      })
    })
  }
  async eval(expression, { awaitPromise = true } = {}) {
    const r = await this.send('Runtime.evaluate', { expression, awaitPromise, returnByValue: true })
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description ?? 'evaluate failed')
    return r.result.value
  }
}

/* ---------- what runs inside the page before any script of the app's ---------- */

const INIT = `(() => {
  try { localStorage.setItem('sirdar.workspaceId', ${JSON.stringify(WORKSPACE)}) } catch {}
  const sd = { commits: [], marks: {}, keys: [] }
  window.__sd = sd
  const seen = new WeakMap()
  const NAMED = new Set([0, 1, 11, 14, 15])
  function nameOf(f) {
    const t = f.type
    if (!t) return '?'
    if (typeof t === 'string') return t
    return t.displayName || t.name || (t.render && (t.render.displayName || t.render.name)) ||
      (t.type && (t.type.displayName || t.type.name)) || 'anonymous'
  }
  function walk(root) {
    let count = 0
    const names = {}
    let f = root.child
    const stack = []
    while (f) {
      if (NAMED.has(f.tag)) {
        let rec = seen.get(f) || (f.alternate && seen.get(f.alternate))
        const rendered = !rec || rec.props !== f.memoizedProps || rec.state !== f.memoizedState
        if (!rec) { rec = {}; seen.set(f, rec); if (f.alternate) seen.set(f.alternate, rec) }
        rec.props = f.memoizedProps
        rec.state = f.memoizedState
        if (rendered) { count++; const n = nameOf(f); names[n] = (names[n] || 0) + 1 }
      }
      if (f.child) { stack.push(f); f = f.child; continue }
      while (f && !f.sibling) f = stack.pop()
      if (f) f = f.sibling
    }
    return { count, names }
  }
  window.__REACT_DEVTOOLS_GLOBAL_HOOK__ = {
    supportsFiber: true,
    renderers: new Map(),
    inject() { return 1 },
    onScheduleFiberRoot() {},
    onCommitFiberUnmount() {},
    onPostCommitFiberRoot() {},
    onCommitFiberRoot(id, root) {
      try {
        const { count, names } = walk(root.current)
        sd.commits.push({ t: performance.now(), count, names })
        if (sd.commits.length > 5000) sd.commits.splice(0, 1000)
      } catch (e) { sd.commits.push({ t: performance.now(), count: -1, error: String(e) }) }
    },
  }
  // The first frame after a DOM condition holds: two animation frames on.
  sd.whenPainted = (test, timeout = 15000) => new Promise((resolve, reject) => {
    const t0 = performance.now()
    const check = () => {
      if (test()) {
        requestAnimationFrame(() => requestAnimationFrame(() => resolve(performance.now())))
        return
      }
      if (performance.now() - t0 > timeout) { reject(new Error('timeout: ' + test.toString())); return }
      requestAnimationFrame(check)
    }
    check()
  })
  // A count that stops moving for \`quiet\` ms.
  sd.whenSettled = (measure, quiet = 500, timeout = 20000) => new Promise((resolve, reject) => {
    const t0 = performance.now()
    let last = measure(), lastAt = performance.now()
    const check = () => {
      const now = performance.now()
      const v = measure()
      if (v !== last) { last = v; lastAt = now }
      if (now - lastAt >= quiet) { resolve({ at: lastAt, value: v }); return }
      if (now - t0 > timeout) { reject(new Error('settle timeout')); return }
      requestAnimationFrame(check)
    }
    check()
  })
})()`

/* ---------- the trace summary ---------- */

function summarise(events) {
  let taskTotal = 0
  let longest = 0
  let over50 = 0
  let layouts = 0
  let styles = 0
  let paints = 0
  let renderer = 0
  for (const e of events) {
    if (e.ph !== 'X' || typeof e.dur !== 'number') continue
    const d = e.dur / 1000
    switch (e.name) {
      case 'RunTask':
        taskTotal += d
        if (d > longest) longest = d
        if (d > 50) over50++
        break
      case 'Layout':
        layouts++
        break
      case 'UpdateLayoutTree':
        styles++
        break
      case 'Paint':
        paints++
        break
      case 'FunctionCall':
      case 'EvaluateScript':
      case 'v8.compile':
        renderer += d
        break
    }
  }
  return { taskTotal, longest, over50, layouts, styles, paints, scripting: renderer }
}

async function traced(cdp, fn) {
  const events = []
  const offData = cdp.on('Tracing.dataCollected', (p) => events.push(...p.value))
  await cdp.send('Tracing.start', {
    traceConfig: {
      includedCategories: ['devtools.timeline', 'disabled-by-default-devtools.timeline', 'blink.user_timing'],
      excludedCategories: ['*'],
    },
    transferMode: 'ReportEvents',
  })
  let result
  try {
    result = await fn()
  } finally {
    const done = cdp.once('Tracing.tracingComplete')
    await cdp.send('Tracing.end')
    await done
    offData()
  }
  return { result, trace: summarise(events) }
}

/* ---------- the scenarios ---------- */

const BOARD = `${URL_BASE}/#/`
const RUN_HASH = `#/runs/${WORKSPACE}/${RUN}`

async function boot(cdp) {
  // Leaving first, so a second boot is a navigation and not a hash change.
  const blank = cdp.once('Page.loadEventFired')
  await cdp.send('Page.navigate', { url: 'about:blank' })
  await blank
  const loaded = cdp.once('Page.loadEventFired')
  await cdp.send('Page.navigate', { url: BOARD })
  await loaded
  const painted = await cdp.eval(`__sd.whenPainted(() => document.querySelector('.sd-lane .sd-run-card'))`)
  const nav = await cdp.eval(`(() => {
    const n = performance.getEntriesByType('navigation')[0]
    const fcp = performance.getEntriesByName('first-contentful-paint')[0]
    return { dcl: n.domContentLoadedEventEnd, fcp: fcp ? fcp.startTime : NaN, transfer: n.transferSize }
  })()`)
  return { boardPainted: painted, ...nav }
}

async function clickNav(cdp, label, test) {
  return cdp.eval(`(async () => {
    const row = [...document.querySelectorAll('.sd-nav-row')].find((b) => b.textContent.trim().startsWith(${JSON.stringify(label)}))
    if (!row) throw new Error('no nav row ' + ${JSON.stringify(label)})
    const t0 = performance.now()
    row.click()
    const t1 = await __sd.whenPainted(() => ${test})
    return t1 - t0
  })()`)
}

/**
 * The transcript's rows, whichever layout is current: Conversation groups
 * them under `.sc-flow`, Document lists the path, Workbench the console.
 */
const ROWS = '.sc-flow > *, .sn-path__scroll .sn-step, .wb-docarea > *'

async function openRun(cdp) {
  return cdp.eval(`(async () => {
    const t0 = performance.now()
    location.hash = ${JSON.stringify(RUN_HASH)}
    const first = await __sd.whenPainted(() => document.querySelectorAll(${JSON.stringify(ROWS)}).length > 0)
    const settled = await __sd.whenSettled(() => document.querySelectorAll(${JSON.stringify(ROWS)}).length)
    return { first: first - t0, settled: settled.at - t0, rows: settled.value }
  })()`)
}

async function hoverRow(cdp, index) {
  const box = await cdp.eval(`(() => {
    const rows = document.querySelectorAll('.sd-session-row')
    const r = rows[${index} % rows.length].getBoundingClientRect()
    return { x: r.left + r.width / 2, y: r.top + r.height / 2 }
  })()`)
  // Park the pointer away from the list first, so entering the row is an entry.
  await cdp.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 700, y: 400 })
  await cdp.eval(`new Promise((r) => setTimeout(r, 400))`)
  await cdp.eval(`__sd.marks.hover = null; document.querySelector('.sd-session-card') && document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))`)
  const t0 = await cdp.eval(`performance.now()`)
  await cdp.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: box.x, y: box.y })
  const t1 = await cdp.eval(`__sd.whenPainted(() => document.querySelector('.sd-session-card'))`)
  await cdp.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 700, y: 400 })
  await cdp.eval(`new Promise((r) => setTimeout(r, 300))`)
  return t1 - t0
}

async function typeFilter(cdp, text) {
  await cdp.eval(`(() => { const i = document.querySelector('.sd-search input'); i.focus(); i.select(); })()`)
  const times = []
  for (const ch of text) {
    const before = await cdp.eval(`(() => {
      const lanes = document.querySelector('.board-lanes')
      __sd.laneHTML = lanes.innerHTML.length
      return performance.now()
    })()`)
    await cdp.send('Input.dispatchKeyEvent', { type: 'keyDown', text: ch, key: ch, unmodifiedText: ch })
    await cdp.send('Input.dispatchKeyEvent', { type: 'keyUp', key: ch })
    const after = await cdp.eval(`__sd.whenPainted(() => document.querySelector('.board-lanes').innerHTML.length !== __sd.laneHTML || document.querySelector('.sd-search input').value.endsWith(${JSON.stringify(ch)}))`)
    times.push(after - before)
  }
  // Clear for the next round.
  await cdp.eval(`(async () => {
    const i = document.querySelector('.sd-search input')
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set
    setter.call(i, '')
    i.dispatchEvent(new Event('input', { bubbles: true }))
    await __sd.whenPainted(() => document.querySelectorAll('.sd-lane .sd-run-card').length > 0)
  })()`)
  return times
}

async function rest(cdp, seconds) {
  await cdp.eval(`__sd.commits.length = 0`)
  await cdp.eval(`new Promise((r) => setTimeout(r, ${seconds * 1000}))`)
  const commits = await cdp.eval(`__sd.commits.slice()`)
  const total = commits.reduce((n, c) => n + Math.max(0, c.count), 0)
  const names = {}
  for (const c of commits) for (const [k, v] of Object.entries(c.names ?? {})) names[k] = (names[k] || 0) + v
  const top = Object.entries(names)
    .sort((a, b) => b[1] - a[1])
    .slice(0, 12)
  return { commits: commits.length, perSecond: commits.length / seconds, rendered: total, perCommit: commits.length ? total / commits.length : 0, top }
}

/* ---------- main ---------- */

async function main() {
  const profile = mkdtempSync(join(tmpdir(), 'sirdar-perf-'))
  const chrome = spawn(
    CHROME,
    [
      '--headless=new',
      `--remote-debugging-port=${PORT}`,
      `--user-data-dir=${profile}`,
      '--window-size=1440,900',
      '--no-first-run',
      '--no-default-browser-check',
      '--disable-background-timer-throttling',
      '--disable-renderer-backgrounding',
      'about:blank',
    ],
    { stdio: 'ignore' },
  )
  const cleanup = () => {
    try {
      chrome.kill('SIGKILL')
    } catch {}
    rmSync(profile, { recursive: true, force: true })
  }
  process.on('exit', cleanup)
  process.on('SIGINT', () => process.exit(130))

  let targets = []
  for (let i = 0; i < 50 && targets.length === 0; i++) {
    await new Promise((r) => setTimeout(r, 200))
    try {
      targets = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).filter((t) => t.type === 'page')
    } catch {}
  }
  if (targets.length === 0) throw new Error('Chrome did not expose a page target')
  const ws = new WebSocket(targets[0].webSocketDebuggerUrl)
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve)
    ws.addEventListener('error', reject)
  })
  const cdp = new CDP(ws)
  await cdp.send('Page.enable')
  await cdp.send('Runtime.enable')
  await cdp.send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 2, mobile: false })
  await cdp.send('Page.addScriptToEvaluateOnNewDocument', { source: INIT })
  // Fonts and the bundle come from the cache after the first load; a cold
  // first boot is reported on its own.
  await cdp.send('Network.enable')

  const out = {}
  console.log(`# sirdar perf — ${URL_BASE}, workspace ${WORKSPACE}, run ${RUN}, ${ROUNDS} rounds`)

  // (a) boot: one cold, then warm rounds.
  {
    const cold = await traced(cdp, () => boot(cdp))
    const warm = []
    const traces = []
    for (let i = 0; i < ROUNDS; i++) {
      const r = await traced(cdp, () => boot(cdp))
      warm.push(r.result)
      traces.push(r.trace)
    }
    out.boot = {
      cold: cold.result,
      coldTrace: cold.trace,
      warm: {
        boardPainted: median(warm.map((w) => w.boardPainted)),
        fcp: median(warm.map((w) => w.fcp)),
        dcl: median(warm.map((w) => w.dcl)),
      },
      warmTrace: { taskTotal: median(traces.map((t) => t.taskTotal)), longest: median(traces.map((t) => t.longest)), over50: median(traces.map((t) => t.over50)) },
      transfer: warm[0].transfer,
    }
    console.log(`boot   cold ${ms(cold.result.boardPainted)} (fcp ${ms(cold.result.fcp)}); warm median ${ms(out.boot.warm.boardPainted)} (fcp ${ms(out.boot.warm.fcp)}, dcl ${ms(out.boot.warm.dcl)}); main-thread ${ms(out.boot.warmTrace.taskTotal)}, longest task ${ms(out.boot.warmTrace.longest)}, tasks>50ms ${out.boot.warmTrace.over50}`)
  }

  // (b) nav: Board → Register → Board, and Board → Eval → Board.
  {
    const toRegister = []
    const toBoard = []
    const toEval = []
    const traces = []
    for (let i = 0; i < ROUNDS; i++) {
      const r = await traced(cdp, async () => {
        const a = await clickNav(cdp, 'Register', `document.querySelector('.sd-page-head__title') && document.querySelector('.sd-page-head__title').textContent.trim() === 'Register'`)
        const b = await clickNav(cdp, 'Board', `document.querySelector('.board-lanes')`)
        const c = await clickNav(cdp, 'Eval', `document.querySelector('.sd-page-head__title') && document.querySelector('.sd-page-head__title').textContent.trim() === 'Eval'`)
        const d = await clickNav(cdp, 'Board', `document.querySelector('.board-lanes')`)
        return { a, b, c, d }
      })
      toRegister.push(r.result.a)
      toBoard.push(r.result.b, r.result.d)
      toEval.push(r.result.c)
      traces.push(r.trace)
    }
    out.nav = { toRegister: median(toRegister), toBoard: median(toBoard), toEval: median(toEval), longest: median(traces.map((t) => t.longest)), layouts: median(traces.map((t) => t.layouts)) }
    console.log(`nav    → Register ${ms(out.nav.toRegister)}, → Eval ${ms(out.nav.toEval)}, → Board ${ms(out.nav.toBoard)}; longest task ${ms(out.nav.longest)}, layouts per round ${out.nav.layouts}`)
  }

  // (c) the long run.
  {
    const firsts = []
    const settleds = []
    const traces = []
    let rows = 0
    for (let i = 0; i < ROUNDS; i++) {
      await clickNav(cdp, 'Board', `document.querySelector('.board-lanes')`)
      await cdp.eval(`new Promise((r) => setTimeout(r, 300))`)
      const r = await traced(cdp, () => openRun(cdp))
      firsts.push(r.result.first)
      settleds.push(r.result.settled)
      rows = r.result.rows
      traces.push(r.trace)
    }
    out.run = { first: median(firsts), settled: median(settleds), rows, longest: median(traces.map((t) => t.longest)), taskTotal: median(traces.map((t) => t.taskTotal)), layouts: median(traces.map((t) => t.layouts)) }
    console.log(`run    first turn painted ${ms(out.run.first)}, transcript settled ${ms(out.run.settled)} (${rows} rows); main-thread ${ms(out.run.taskTotal)}, longest task ${ms(out.run.longest)}, layouts ${out.run.layouts}`)
  }

  // (d) hover on a sessions row (from the board).
  {
    await clickNav(cdp, 'Board', `document.querySelector('.board-lanes')`)
    await cdp.eval(`new Promise((r) => setTimeout(r, 400))`)
    const times = []
    const traces = []
    for (let i = 0; i < ROUNDS; i++) {
      const r = await traced(cdp, () => hoverRow(cdp, i))
      times.push(r.result)
      traces.push(r.trace)
    }
    out.hover = { card: median(times), longest: median(traces.map((t) => t.longest)), layouts: median(traces.map((t) => t.layouts)) }
    console.log(`hover  row → card painted ${ms(out.hover.card)} (includes the open delay); longest task ${ms(out.hover.longest)}, layouts ${out.hover.layouts}`)
  }

  // (e) typing in the board filter.
  {
    const times = []
    const traces = []
    for (let i = 0; i < ROUNDS; i++) {
      const r = await traced(cdp, () => typeFilter(cdp, 'sbx-1'))
      times.push(...r.result)
      traces.push(r.trace)
    }
    out.type = { keystroke: median(times), worst: Math.max(...times), longest: median(traces.map((t) => t.longest)) }
    console.log(`type   keystroke → lanes painted median ${ms(out.type.keystroke)}, worst ${ms(out.type.worst)}; longest task ${ms(out.type.longest)}`)
  }

  // (f) at rest on the board.
  {
    await cdp.eval(`new Promise((r) => setTimeout(r, 500))`)
    const r = await rest(cdp, REST_S)
    out.rest = r
    console.log(`rest   board, ${REST_S}s: ${r.commits} commits (${r.perSecond.toFixed(2)}/s), ${r.rendered} component renders, ${r.perCommit.toFixed(1)} per commit`)
    if (r.top.length) console.log(`       most rendered: ${r.top.map(([n, c]) => `${n}×${c}`).join(', ')}`)
  }

  // (g) at rest on the long run.
  {
    await openRun(cdp)
    await cdp.eval(`new Promise((r) => setTimeout(r, 500))`)
    const r = await rest(cdp, Math.min(REST_S, 10))
    out.restRun = r
    console.log(`rest   run, ${Math.min(REST_S, 10)}s: ${r.commits} commits (${r.perSecond.toFixed(2)}/s), ${r.rendered} component renders`)
    if (r.top.length) console.log(`       most rendered: ${r.top.map(([n, c]) => `${n}×${c}`).join(', ')}`)
  }

  if (args.json) console.log(JSON.stringify(out, null, 2))
  ws.close()
  process.exit(0)
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
