#!/usr/bin/env node
/**
 * Timing the Session window in a headless Chromium over the DevTools protocol.
 *
 *   node scripts/session-trace.mjs --url http://127.0.0.1:47391 --workspace <id> --run <runId> \
 *     [--chrome "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser"] [--rounds 5] \
 *     [--layout conversation|workbench|document] [--json]
 *     [--stream <run dir> --from <events.jsonl> [--drip <ms> --lines <n> | --batch <n> --bursts <n>]]
 *
 * `scripts/perf-trace.mjs` times the shell — boot, sidebar navigation, the
 * board. This one stays inside one run and asks what the reader feels there:
 *
 *   open    the address set to the run → the first painted turn, and the
 *           moment the transcript stops growing
 *   scroll  a wheel roll through the whole transcript and back, sampled
 *           frame by frame in the page: how long each frame took, how many
 *           went over one 60Hz vsync and over two
 *   type    one keystroke in the composer → the frame that shows the letter
 *   layout  the header's layout switcher → the first frame of the new layout
 *   hover   the pointer resting on a sessions row → the first frame with its card
 *   stream  with --stream <run dir> --from <log>, lines appended to a running
 *           run's events.jsonl: the commits, the component renders and the
 *           main-thread time each burst costs, and the frames it drops
 *   drip    the same replay one line every --drip ms, as a provider writes
 *           them, reported from the window's own clock: how often the
 *           transcript actually changed and how far apart those changes fell
 *
 * React commits are counted through the DevTools hook, which production
 * React calls on every commit; a component counts as rendered when its props
 * or hook state object is a new reference. Names come from the bundle, so
 * build with `--minify false` when the breakdown matters more than the
 * numbers. A sampling CPU profile runs alongside the scroll and the stream,
 * and the heaviest self-time frames are printed with it.
 *
 * Nothing here starts a run; --stream only appends to a log this script's
 * caller prepared.
 */
import { mkdtempSync, rmSync, readFileSync, appendFileSync } from 'node:fs'
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
const URL_BASE = (args.url ?? 'http://127.0.0.1:47391').replace(/\/$/, '')
const WORKSPACE = args.workspace
const RUN = args.run
const CHROME = args.chrome ?? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
const ROUNDS = Number(args.rounds ?? 5)
const LAYOUT = args.layout ?? 'conversation'
const PORT = Number(args.port ?? 9366)
/** A run directory whose events.jsonl this script appends to, to replay a live stream. */
const STREAM_DIR = args.stream && args.stream !== 'true' ? args.stream : ''
/** Lines per append, and how many appends. A real run's watcher polls every 500ms. */
const STREAM_BATCH = Number(args.batch ?? 12)
const STREAM_BURSTS = Number(args.bursts ?? 12)
const STREAM_FROM = args.from && args.from !== 'true' ? args.from : ''
/** With --drip <ms>, lines are appended one at a time that far apart, as a provider writes them. */
const DRIP_MS = args.drip && args.drip !== 'true' ? Number(args.drip) : 0
const DRIP_LINES = Number(args.lines ?? 120)

if (!WORKSPACE || !RUN) {
  console.error('need --workspace <id> and --run <runId>')
  process.exit(2)
}

const median = (xs) => {
  const s = xs.slice().sort((a, b) => a - b)
  return s.length === 0 ? NaN : s.length % 2 ? s[(s.length - 1) / 2] : (s[s.length / 2 - 1] + s[s.length / 2]) / 2
}
const pct = (xs, p) => {
  const s = xs.slice().sort((a, b) => a - b)
  return s.length === 0 ? NaN : s[Math.min(s.length - 1, Math.floor((p / 100) * s.length))]
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
  try {
    localStorage.setItem('sirdar.workspaceId', ${JSON.stringify(WORKSPACE)})
    localStorage.setItem('sirdar.sessionLayout', ${JSON.stringify(LAYOUT)})
  } catch {}
  const sd = { commits: [], frames: [], sampling: false }
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
    setStrictMode() {},
    onCommitFiberRoot(id, root) {
      try {
        const { count, names } = walk(root.current)
        sd.commits.push({ t: performance.now(), count, names })
        if (sd.commits.length > 5000) sd.commits.splice(0, 1000)
      } catch (e) { sd.commits.push({ t: performance.now(), count: -1, error: String(e) }) }
    },
  }

  /* Frame intervals, sampled only while a scenario asks for them: the gap
     between two animation frames is what a scroll or a stream is felt as. */
  sd.startFrames = () => {
    sd.frames = []
    sd.sampling = true
    let last = performance.now()
    const tick = (now) => {
      if (!sd.sampling) return
      sd.frames.push(now - last)
      last = now
      requestAnimationFrame(tick)
    }
    requestAnimationFrame(tick)
  }
  sd.stopFrames = () => {
    sd.sampling = false
    // The first sample spans the call, not a frame.
    return sd.frames.slice(1)
  }

  sd.whenPainted = (test, timeout = 20000) => new Promise((resolve, reject) => {
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
  sd.whenSettled = (measure, quiet = 500, timeout = 25000) => new Promise((resolve, reject) => {
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

/* ---------- trace and profile summaries ---------- */

function summarise(events) {
  let taskTotal = 0
  let longest = 0
  let over50 = 0
  let layouts = 0
  let styles = 0
  let paints = 0
  let scripting = 0
  const long = []
  for (const e of events) {
    if (e.ph !== 'X' || typeof e.dur !== 'number') continue
    const d = e.dur / 1000
    switch (e.name) {
      case 'RunTask':
        taskTotal += d
        if (d > longest) longest = d
        if (d > 50) { over50++; long.push(d) }
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
        scripting += d
        break
    }
  }
  return { taskTotal, longest, over50, longTasks: long.sort((a, b) => b - a).slice(0, 5), layouts, styles, paints, scripting }
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

/** Self time per function from a sampling profile, heaviest first. */
function hotFrames(profile, limit = 10) {
  if (!profile || !profile.nodes) return []
  const byId = new Map(profile.nodes.map((n) => [n.id, n]))
  const self = new Map()
  const total = profile.timeDeltas?.reduce((n, d) => n + Math.max(0, d), 0) ?? 0
  for (let i = 0; i < (profile.samples?.length ?? 0); i++) {
    const node = byId.get(profile.samples[i])
    if (!node) continue
    const f = node.callFrame
    const key = `${f.functionName || '(anonymous)'} ${String(f.url).split('/').pop()}:${f.lineNumber + 1}`
    self.set(key, (self.get(key) ?? 0) + Math.max(0, profile.timeDeltas[i] ?? 0) / 1000)
  }
  return [...self.entries()]
    .sort((a, b) => b[1] - a[1])
    .slice(0, limit)
    .map(([k, v]) => ({ frame: k, ms: v, share: total ? (v * 1000) / total : 0 }))
}

async function profiled(cdp, fn) {
  await cdp.send('Profiler.enable')
  await cdp.send('Profiler.setSamplingInterval', { interval: 200 })
  await cdp.send('Profiler.start')
  let result
  try {
    result = await fn()
  } finally {
    var { profile } = await cdp.send('Profiler.stop')
  }
  return { result, hot: hotFrames(profile) }
}

/* ---------- the scenarios ---------- */

const BOARD = `${URL_BASE}/#/`
const RUN_HASH = `#/runs/${WORKSPACE}/${RUN}`
/**
 * Conversation is the default layout and the one the deep scenarios run in;
 * the other two are only opened and timed, through the roots below.
 */
const STREAM_SEL = `.sc-stream, .sn-path__scroll, .wb-docarea`
const ROW_SEL = `.sc-flow > *, .sn-path__scroll .sn-step, .wb-docarea > *`
const LAYOUT_ROOT = {
  conversation: `.sc[data-layout="conversation"]`,
  document: `[data-testid="session-document"]`,
  workbench: `[data-testid="session-workbench"]`,
}

async function openBoard(cdp) {
  await cdp.eval(`(async () => {
    location.hash = '#/'
    await __sd.whenPainted(() => document.querySelector('.board-lanes'))
  })()`)
  await cdp.eval(`new Promise((r) => setTimeout(r, 300))`)
}

async function openRun(cdp) {
  return cdp.eval(`(async () => {
    const t0 = performance.now()
    location.hash = ${JSON.stringify(RUN_HASH)}
    const first = await __sd.whenPainted(() => document.querySelectorAll(${JSON.stringify(ROW_SEL)}).length > 0)
    const settled = await __sd.whenSettled(() => document.querySelectorAll(${JSON.stringify(ROW_SEL)}).length)
    return { first: first - t0, settled: settled.at - t0, rows: settled.value, nodes: document.querySelectorAll(${JSON.stringify(STREAM_SEL)})[0]?.querySelectorAll('*').length ?? 0 }
  })()`)
}

/** A wheel roll from the top of the transcript to the bottom and back, frame by frame. */
async function scrollStream(cdp) {
  const box = await cdp.eval(`(() => {
    const el = document.querySelector(${JSON.stringify(STREAM_SEL)})
    if (!el) throw new Error('no transcript element')
    const r = el.getBoundingClientRect()
    return { x: r.left + r.width / 2, y: r.top + r.height / 2, height: el.scrollHeight, view: el.clientHeight }
  })()`)
  await cdp.eval(`(() => {
    const el = document.querySelector(${JSON.stringify(STREAM_SEL)})
    el.scrollTop = 0
    __sd.travelled = 0
    __sd.lastTop = 0
    el.addEventListener('scroll', () => {
      __sd.travelled += Math.abs(el.scrollTop - __sd.lastTop)
      __sd.lastTop = el.scrollTop
    })
  })()`)
  await cdp.eval(`new Promise((r) => setTimeout(r, 200))`)
  await cdp.eval(`__sd.startFrames()`)
  const steps = 40
  const delta = Math.ceil(box.height / steps)
  for (let dir of [1, -1]) {
    for (let i = 0; i < steps; i++) {
      await cdp.send('Input.dispatchMouseEvent', {
        type: 'mouseWheel',
        x: box.x,
        y: box.y,
        deltaX: 0,
        deltaY: dir * delta,
      })
      await cdp.eval(`new Promise((r) => requestAnimationFrame(r))`)
    }
  }
  const frames = await cdp.eval(`__sd.stopFrames()`)
  const travelled = await cdp.eval(`__sd.travelled`)
  return { frames, height: box.height, view: box.view, travelled }
}

async function typeComposer(cdp, text) {
  const ready = await cdp.eval(`(() => {
    const box = document.querySelector('.session-composer textarea, .composer textarea')
    if (!box || box.disabled) return false
    box.focus()
    return true
  })()`)
  if (!ready) return null
  const times = []
  const commits = []
  for (const ch of text) {
    const before = await cdp.eval(`(() => { __sd.commits.length = 0; __sd.len = document.querySelector('.session-composer textarea, .composer textarea').value.length; return performance.now() })()`)
    await cdp.send('Input.dispatchKeyEvent', { type: 'keyDown', text: ch, key: ch, unmodifiedText: ch })
    await cdp.send('Input.dispatchKeyEvent', { type: 'keyUp', key: ch })
    const after = await cdp.eval(`__sd.whenPainted(() => document.querySelector('.session-composer textarea, .composer textarea').value.length !== __sd.len)`)
    times.push(after - before)
    const c = await cdp.eval(`({ commits: __sd.commits.length, rendered: __sd.commits.reduce((n, c) => n + Math.max(0, c.count), 0), top: Object.entries(__sd.commits.reduce((m, c) => { for (const [k, v] of Object.entries(c.names ?? {})) m[k] = (m[k] || 0) + v; return m }, {})).sort((a, b) => b[1] - a[1]).slice(0, 6) })`)
    commits.push(c)
  }
  // Leave the box empty for the next round.
  await cdp.eval(`(() => {
    const box = document.querySelector('.session-composer textarea, .composer textarea')
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set
    setter.call(box, '')
    box.dispatchEvent(new Event('input', { bubbles: true }))
  })()`)
  return { times, commits }
}

/** The header's three-icon switcher: conversation → workbench → document → conversation. */
async function switchLayouts(cdp) {
  const out = {}
  for (const name of ['workbench', 'document', 'conversation']) {
    const t = await cdp.eval(`(async () => {
      const root = ${JSON.stringify(LAYOUT_ROOT[name])}
      const buttons = [...document.querySelectorAll('.sn-switch [role="radio"], .sn-switch button')]
      const b = buttons.find((x) => (x.getAttribute('title') || x.getAttribute('aria-label') || '').toLowerCase().startsWith(${JSON.stringify(name)}))
      if (!b) throw new Error('no switcher button for ' + ${JSON.stringify(name)})
      const t0 = performance.now()
      b.click()
      const t1 = await __sd.whenPainted(() => document.querySelector(root))
      return t1 - t0
    })()`)
    out[name] = t
    await cdp.eval(`new Promise((r) => setTimeout(r, 250))`)
  }
  return out
}

async function hoverRow(cdp, index) {
  const box = await cdp.eval(`(() => {
    const rows = document.querySelectorAll('.sd-session-row')
    if (rows.length === 0) return null
    const r = rows[${index} % rows.length].getBoundingClientRect()
    return { x: r.left + r.width / 2, y: r.top + r.height / 2 }
  })()`)
  if (!box) return NaN
  await cdp.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 900, y: 500 })
  await cdp.eval(`new Promise((r) => setTimeout(r, 400))`)
  const t0 = await cdp.eval(`performance.now()`)
  await cdp.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: box.x, y: box.y })
  const t1 = await cdp.eval(`__sd.whenPainted(() => document.querySelector('.sd-session-card'))`)
  await cdp.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 900, y: 500 })
  await cdp.eval(`new Promise((r) => setTimeout(r, 300))`)
  return t1 - t0
}

/**
 * Replays a recorded log into a run the watcher is tailing: `--stream <dir>`
 * is a run directory whose state.json says running, `--from <file>` the log
 * the lines are taken from. One append per burst, so what the window sees is
 * what a real run's 500ms poll delivers.
 */
/**
 * One line every `DRIP_MS`, as a provider writes them, with the window's own
 * commit clock as the witness: how many commits the window actually makes,
 * how far apart they land, and how many lines each one carries. This is what
 * says whether the transcript streams or arrives in jumps.
 */
async function dripInto(cdp, dir, from) {
  const source = readFileSync(from, 'utf8').split('\n').filter(Boolean)
  const target = join(dir, 'events.jsonl')
  const already = readFileSync(target, 'utf8').split('\n').filter(Boolean).length
  await cdp.eval(`__sd.commits.length = 0; __sd.startFrames(); __sd.rows = []; __sd.rowTimer = setInterval(() => { const el = document.querySelector(${JSON.stringify(STREAM_SEL)}); __sd.rows.push([performance.now(), el ? el.textContent.length : 0, el ? el.querySelectorAll('*').length : 0]) }, 16)`)
  const wrote = []
  const t0 = Date.now()
  for (let i = 0; i < DRIP_LINES; i++) {
    const line = source[already + i]
    if (!line) break
    appendFileSync(target, line + '\n')
    wrote.push(Date.now() - t0)
    await new Promise((r) => setTimeout(r, DRIP_MS))
  }
  await new Promise((r) => setTimeout(r, 2500))
  const page = await cdp.eval(`(() => {
    clearInterval(__sd.rowTimer)
    const rows = __sd.rows
    const jumps = []
    for (let i = 1; i < rows.length; i++) if (rows[i][1] !== rows[i - 1][1] || rows[i][2] !== rows[i - 1][2]) jumps.push([rows[i][0], rows[i][1] - rows[i - 1][1], rows[i][2] - rows[i - 1][2]])
    return { commits: __sd.commits.map((c) => [c.t, c.count]), jumps, frames: __sd.stopFrames() }
  })()`)
  return { wrote, ...page }
}

async function streamInto(cdp, dir, from) {
  const source = readFileSync(from, 'utf8').split('\n').filter(Boolean)
  const target = join(dir, 'events.jsonl')
  const already = readFileSync(target, 'utf8').split('\n').filter(Boolean).length
  const bursts = []
  await cdp.eval(`__sd.startFrames(); __sd.commits.length = 0`)
  for (let b = 0; b < STREAM_BURSTS; b++) {
    const slice = source.slice(already + b * STREAM_BATCH, already + (b + 1) * STREAM_BATCH)
    if (slice.length === 0) break
    const before = await cdp.eval(`({ rows: document.querySelectorAll(${JSON.stringify(ROW_SEL)}).length, commits: __sd.commits.length })`)
    appendFileSync(target, slice.join('\n') + '\n')
    const grew = await cdp
      .eval(`__sd.whenPainted(() => document.querySelectorAll(${JSON.stringify(ROW_SEL)}).length !== ${before.rows}, 8000).then((t) => t).catch(() => -1)`)
      .catch(() => -1)
    const after = await cdp.eval(`({
      commits: __sd.commits.slice(${before.commits}).length,
      rendered: __sd.commits.slice(${before.commits}).reduce((n, c) => n + Math.max(0, c.count), 0),
      names: __sd.commits.slice(${before.commits}).reduce((m, c) => { for (const [k, v] of Object.entries(c.names ?? {})) m[k] = (m[k] || 0) + v; return m }, {}),
    })`)
    bursts.push({ lines: slice.length, painted: grew, ...after })
    await cdp.eval(`new Promise((r) => setTimeout(r, 250))`)
  }
  const frames = await cdp.eval(`__sd.stopFrames()`)
  return { bursts, frames }
}

/* ---------- main ---------- */

async function main() {
  const profile = mkdtempSync(join(tmpdir(), 'sirdar-session-'))
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
  if (targets.length === 0) throw new Error('the browser did not expose a page target')
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

  const loaded = cdp.once('Page.loadEventFired')
  await cdp.send('Page.navigate', { url: BOARD })
  await loaded
  await cdp.eval(`__sd.whenPainted(() => document.querySelector('.board-lanes'), 25000)`)
  await cdp.eval(`new Promise((r) => setTimeout(r, 500))`)

  const out = { url: URL_BASE, workspace: WORKSPACE, run: RUN, layout: LAYOUT }
  console.log(`# sirdar session perf — ${URL_BASE}, run ${RUN}, layout ${LAYOUT}, ${ROUNDS} rounds`)

  // (a) opening the run.
  {
    const firsts = []
    const settleds = []
    const traces = []
    let rows = 0
    let nodes = 0
    for (let i = 0; i < ROUNDS; i++) {
      await openBoard(cdp)
      const r = await traced(cdp, () => openRun(cdp))
      firsts.push(r.result.first)
      settleds.push(r.result.settled)
      rows = r.result.rows
      nodes = r.result.nodes
      traces.push(r.trace)
    }
    out.open = {
      first: median(firsts),
      settled: median(settleds),
      rows,
      nodes,
      longest: median(traces.map((t) => t.longest)),
      taskTotal: median(traces.map((t) => t.taskTotal)),
      over50: median(traces.map((t) => t.over50)),
      worstTasks: traces.flatMap((t) => t.longTasks).sort((a, b) => b - a).slice(0, 5),
      layouts: median(traces.map((t) => t.layouts)),
    }
    console.log(
      `open   first row painted ${ms(out.open.first)}, settled ${ms(out.open.settled)} (${rows} rows, ${nodes} nodes); main-thread ${ms(out.open.taskTotal)}, longest task ${ms(out.open.longest)}, tasks>50ms ${out.open.over50} [${out.open.worstTasks.map((x) => x.toFixed(0)).join(', ')}]`,
    )
  }

  // (b) scrolling the transcript.
  {
    const all = []
    const traces = []
    let hot = []
    let geom = {}
    for (let i = 0; i < ROUNDS; i++) {
      const r = await traced(cdp, () => (i === 0 ? profiled(cdp, () => scrollStream(cdp)) : scrollStream(cdp)))
      const res = i === 0 ? r.result.result : r.result
      if (i === 0) hot = r.result.hot
      all.push(...res.frames)
      geom = { height: res.height, view: res.view, travelled: res.travelled }
      traces.push(r.trace)
    }
    const slow = all.filter((f) => f > 16.7).length
    const stalled = all.filter((f) => f > 33.4).length
    out.scroll = {
      frames: all.length,
      median: median(all),
      p95: pct(all, 95),
      worst: Math.max(...all),
      overOneVsync: slow,
      overTwoVsync: stalled,
      ...geom,
      longest: median(traces.map((t) => t.longest)),
      over50: traces.reduce((n, t) => n + t.over50, 0),
      layouts: median(traces.map((t) => t.layouts)),
      styles: median(traces.map((t) => t.styles)),
      paints: median(traces.map((t) => t.paints)),
      hot,
    }
    console.log(
      `scroll ${all.length} frames, ${geom.travelled}px travelled of ${geom.height}px: median ${ms(out.scroll.median)}, p95 ${ms(out.scroll.p95)}, worst ${ms(out.scroll.worst)}; ${slow} frames > 16.7ms (${((100 * slow) / all.length).toFixed(0)}%), ${stalled} > 33.4ms; per round: layouts ${out.scroll.layouts}, styles ${out.scroll.styles}, paints ${out.scroll.paints}, longest task ${ms(out.scroll.longest)}`,
    )
    if (hot.length) console.log(`       hottest: ${hot.slice(0, 6).map((h) => `${h.frame} ${h.ms.toFixed(0)}ms`).join(' | ')}`)
  }

  // (c) typing in the composer.
  {
    const times = []
    const commits = []
    const traces = []
    for (let i = 0; i < ROUNDS; i++) {
      const r = await traced(cdp, () => typeComposer(cdp, 'check the return path'))
      if (!r.result) {
        console.log('type   the composer is disabled on this run; skipped')
        break
      }
      times.push(...r.result.times)
      commits.push(...r.result.commits)
      traces.push(r.trace)
    }
    if (times.length) {
      const names = {}
      for (const c of commits) for (const [k, v] of c.top) names[k] = (names[k] ?? 0) + v
      out.type = {
        keystroke: median(times),
        p95: pct(times, 95),
        worst: Math.max(...times),
        commits: median(commits.map((c) => c.commits)),
        rendered: median(commits.map((c) => c.rendered)),
        top: Object.entries(names).sort((a, b) => b[1] - a[1]).slice(0, 8),
        layouts: median(traces.map((t) => t.layouts)),
        longest: median(traces.map((t) => t.longest)),
      }
      console.log(
        `type   keystroke → painted median ${ms(out.type.keystroke)}, p95 ${ms(out.type.p95)}, worst ${ms(out.type.worst)}; ${out.type.commits} commits and ${out.type.rendered} component renders per key; layouts ${out.type.layouts}, longest task ${ms(out.type.longest)}`,
      )
      console.log(`       rendered per key: ${out.type.top.map(([n, c]) => `${n}×${(c / times.length).toFixed(1)}`).join(', ')}`)
    }
  }

  // (d) the layout switcher.
  {
    const rounds = []
    const traces = []
    for (let i = 0; i < Math.min(ROUNDS, 3); i++) {
      try {
        const r = await traced(cdp, () => switchLayouts(cdp))
        rounds.push(r.result)
        traces.push(r.trace)
      } catch (err) {
        console.log(`layout skipped: ${err.message}`)
        break
      }
    }
    if (rounds.length) {
      out.layout = {
        workbench: median(rounds.map((r) => r.workbench)),
        document: median(rounds.map((r) => r.document)),
        conversation: median(rounds.map((r) => r.conversation)),
        longest: median(traces.map((t) => t.longest)),
      }
      console.log(
        `layout → Workbench ${ms(out.layout.workbench)}, → Document ${ms(out.layout.document)}, → Conversation ${ms(out.layout.conversation)}; longest task ${ms(out.layout.longest)}`,
      )
    }
  }

  // (e) the hover card, from inside the session.
  {
    const times = []
    const traces = []
    for (let i = 0; i < ROUNDS; i++) {
      const r = await traced(cdp, () => hoverRow(cdp, i))
      if (Number.isNaN(r.result)) break
      times.push(r.result)
      traces.push(r.trace)
    }
    if (times.length) {
      out.hover = { card: median(times), worst: Math.max(...times), longest: median(traces.map((t) => t.longest)), layouts: median(traces.map((t) => t.layouts)) }
      console.log(`hover  row → card painted ${ms(out.hover.card)} (includes the open delay), worst ${ms(out.hover.worst)}; longest task ${ms(out.hover.longest)}, layouts ${out.hover.layouts}`)
    }
  }

  // (f) a live stream replayed into a running run, one line at a time.
  if (STREAM_DIR && STREAM_FROM && DRIP_MS > 0) {
    await cdp.eval(`location.hash = ${JSON.stringify(RUN_HASH)}`)
    await cdp.eval(`new Promise((r) => setTimeout(r, 1500))`)
    const r = await traced(cdp, () => dripInto(cdp, STREAM_DIR, STREAM_FROM))
    const { wrote, commits, jumps, frames } = r.result
    const gaps = jumps.slice(1).map((j, i) => j[0] - jumps[i][0])
    const rowsAdded = jumps.reduce((n, j) => n + Math.max(0, j[1]), 0)
    out.drip = {
      lines: wrote.length,
      everyMs: DRIP_MS,
      spanMs: wrote.length ? wrote[wrote.length - 1] : 0,
      commits: commits.length,
      repaintsOfTheList: jumps.length,
      rowsAdded,
      gapMedian: median(gaps),
      gapP95: pct(gaps, 95),
      gapWorst: gaps.length ? Math.max(...gaps) : NaN,
      biggestJump: jumps.length ? Math.max(...jumps.map((j) => j[1])) : 0,
      nodesAdded: jumps.reduce((n, j) => n + Math.max(0, j[2] ?? 0), 0),
      frameWorst: frames.length ? Math.max(...frames) : NaN,
      overTwoVsync: frames.filter((f) => f > 33.4).length,
      longest: r.trace.longest,
      over50: r.trace.over50,
    }
    console.log(
      `drip   ${wrote.length} lines one every ${DRIP_MS}ms over ${(out.drip.spanMs / 1000).toFixed(1)}s: the transcript changed ${jumps.length} times (${out.drip.nodesAdded} nodes added), ${out.drip.commits} commits; gap between changes median ${ms(out.drip.gapMedian)}, p95 ${ms(out.drip.gapP95)}, worst ${ms(out.drip.gapWorst)}; biggest single jump ${out.drip.biggestJump} characters of transcript; worst frame ${ms(out.drip.frameWorst)}, longest task ${ms(out.drip.longest)}`,
    )
  } else if (STREAM_DIR && STREAM_FROM) {
    await cdp.eval(`location.hash = ${JSON.stringify(RUN_HASH)}`)
    await cdp.eval(`new Promise((r) => setTimeout(r, 1500))`)
    const r = await traced(cdp, () => profiled(cdp, () => streamInto(cdp, STREAM_DIR, STREAM_FROM)))
    const { bursts, frames } = r.result.result
    const painted = bursts.map((b) => b.painted).filter((x) => x > 0)
    const names = {}
    for (const b of bursts) for (const [k, v] of Object.entries(b.names ?? {})) names[k] = (names[k] ?? 0) + v
    const lines = bursts.reduce((n, b) => n + b.lines, 0)
    const rendered = bursts.reduce((n, b) => n + b.rendered, 0)
    const commits = bursts.reduce((n, b) => n + b.commits, 0)
    out.stream = {
      bursts: bursts.length,
      lines,
      commits,
      rendered,
      renderedPerLine: lines ? rendered / lines : 0,
      commitsPerBurst: bursts.length ? commits / bursts.length : 0,
      frameMedian: median(frames),
      frameP95: pct(frames, 95),
      frameWorst: Math.max(...frames),
      overTwoVsync: frames.filter((f) => f > 33.4).length,
      mainThread: r.trace.taskTotal,
      longest: r.trace.longest,
      over50: r.trace.over50,
      worstTasks: r.trace.longTasks,
      top: Object.entries(names).sort((a, b) => b[1] - a[1]).slice(0, 10),
      hot: r.result.hot,
    }
    console.log(
      `stream ${bursts.length} bursts, ${lines} lines: ${commits} commits (${out.stream.commitsPerBurst.toFixed(1)}/burst), ${rendered} component renders (${out.stream.renderedPerLine.toFixed(1)}/line); frames median ${ms(out.stream.frameMedian)}, p95 ${ms(out.stream.frameP95)}, worst ${ms(out.stream.frameWorst)}, ${out.stream.overTwoVsync} over 33.4ms; main-thread ${ms(out.stream.mainThread)}, longest task ${ms(out.stream.longest)}, tasks>50ms ${out.stream.over50} [${out.stream.worstTasks.map((x) => x.toFixed(0)).join(', ')}]`,
    )
    console.log(`       most rendered: ${out.stream.top.map(([n, c]) => `${n}×${c}`).join(', ')}`)
    if (out.stream.hot.length) console.log(`       hottest: ${out.stream.hot.slice(0, 6).map((h) => `${h.frame} ${h.ms.toFixed(0)}ms`).join(' | ')}`)
  }

  if (args.json) console.log(JSON.stringify(out, null, 2))
  ws.close()
  process.exit(0)
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
