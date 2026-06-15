// Generates themed SVG charts from results/*.csv. No dependencies.
//
//   node charts/gen.mjs            # writes charts/*.svg from results/*.csv
//
// Pass an extra output dir to also copy the SVGs somewhere (e.g. a blog):
//
//   node charts/gen.mjs /path/to/blog/public/blog
import { readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const repo = join(here, '..')

function csv(path) {
  const [head, ...rows] = readFileSync(path, 'utf8').trim().split('\n')
  const cols = head.split(',')
  return rows.map(r => Object.fromEntries(r.split(',').map((v, i) => [cols[i], v])))
}
const fanout = csv(join(repo, 'results/fanout.csv'))
const startup = csv(join(repo, 'results/startup.csv'))

const pick = (rows, lib, x, y) => rows.filter(r => r.lib === lib).map(r => [Number(r[x]), Number(r[y])])
const emapConns = pick(fanout, 'emap', 'n', 'accepts')
const emersionConns = pick(fanout, 'emersion', 'n', 'accepts')
const emapRss = pick(fanout, 'emap', 'n', 'rss_kb').map(([x, y]) => [x, y / 1024])
const emersionRss = pick(fanout, 'emersion', 'n', 'rss_kb').map(([x, y]) => [x, y / 1024])
const emapReady = pick(startup, 'emap', 'backlog', 'ready_ms').filter(([x]) => x >= 1000)
const emersionReady = pick(startup, 'emersion', 'backlog', 'ready_ms').filter(([x]) => x >= 1000)
const emersion1M = startup.find(r => r.lib === 'emersion' && Number(r.backlog) === 1000000)

const C = {
  bg: '#111113', grid: '#27272a', dim: '#52525b', text: '#fafafa', sub: '#a1a1aa',
  emap: '#22c55e', emapFill: 'rgba(34,197,94,0.12)',
  emersion: '#ef4444', emersionFill: 'rgba(239,68,68,0.10)', cap: '#eab308',
}
const FONT = "'Fira Code', ui-monospace, monospace"
const W = 900, H = 500, M = { l: 78, r: 28, t: 64, b: 70 }
const PW = W - M.l - M.r, PH = H - M.t - M.b
const lin = (v, d0, d1, p0, p1) => p0 + (v - d0) / (d1 - d0) * (p1 - p0)
const log = v => Math.log10(v)
const esc = s => String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;')

function frame(title, subtitle) {
  return `<rect width="${W}" height="${H}" fill="${C.bg}"/>
<text x="${M.l}" y="30" fill="${C.text}" font-family="${FONT}" font-size="19" font-weight="700">${esc(title)}</text>
<text x="${M.l}" y="50" fill="${C.sub}" font-family="${FONT}" font-size="12.5">${esc(subtitle)}</text>`
}
function legend(items) {
  let x = M.l, y = H - 16, out = ''
  for (const it of items) {
    out += `<line x1="${x}" y1="${y - 4}" x2="${x + 26}" y2="${y - 4}" stroke="${it.color}" stroke-width="3"/>`
    out += `<text x="${x + 33}" y="${y}" fill="${C.sub}" font-family="${FONT}" font-size="12.5">${esc(it.label)}</text>`
    x += 40 + it.label.length * 7.4 + 24
  }
  return out
}
function poly(pts, color, fill) {
  const d = pts.map(p => `${p[0].toFixed(1)},${p[1].toFixed(1)}`).join(' ')
  let out = ''
  if (fill) out += `<polygon points="${M.l},${M.t + PH} ${d} ${M.l + PW},${M.t + PH}" fill="${fill}"/>`
  out += `<polyline points="${d}" fill="none" stroke="${color}" stroke-width="2.5"/>`
  for (const p of pts) out += `<circle cx="${p[0].toFixed(1)}" cy="${p[1].toFixed(1)}" r="3.2" fill="${color}"/>`
  return out
}
const wrap = inner => `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${W} ${H}" width="${W}" height="${H}" role="img">\n${inner}\n</svg>\n`

function connections() {
  const X = v => lin(v, 0, 2000, M.l, M.l + PW), Y = v => lin(v, 0, 2000, M.t + PH, M.t)
  let g = frame('Open IMAP connections vs. number of task-watchers',
    'One shared catch-all inbox. Lower is better. Measured against a localhost IMAP server.')
  for (const t of [0, 500, 1000, 1500, 2000]) {
    g += `<line x1="${M.l}" y1="${Y(t)}" x2="${M.l + PW}" y2="${Y(t)}" stroke="${C.grid}"/>`
    g += `<text x="${M.l - 10}" y="${Y(t) + 4}" fill="${C.dim}" font-family="${FONT}" font-size="11" text-anchor="end">${t}</text>`
    g += `<text x="${X(t)}" y="${M.t + PH + 20}" fill="${C.dim}" font-family="${FONT}" font-size="11" text-anchor="middle">${t}</text>`
  }
  g += `<text x="${M.l + PW / 2}" y="${H - 38}" fill="${C.sub}" font-family="${FONT}" font-size="12" text-anchor="middle">task-watchers (N)</text>`
  g += `<text transform="translate(20,${M.t + PH / 2}) rotate(-90)" fill="${C.sub}" font-family="${FONT}" font-size="12" text-anchor="middle">open connections</text>`
  g += `<line x1="${M.l}" y1="${Y(15)}" x2="${M.l + PW}" y2="${Y(15)}" stroke="${C.cap}" stroke-width="1.3" stroke-dasharray="6 4"/>`
  g += `<text x="${M.l + 8}" y="${Y(15) - 8}" fill="${C.cap}" font-family="${FONT}" font-size="11" text-anchor="start">Gmail per-account cap = 15  →  emersion breaks at N=16</text>`
  g += poly(emersionConns.map(([x, y]) => [X(x), Y(y)]), C.emersion)
  g += poly(emapConns.map(([x, y]) => [X(x), Y(y)]), C.emap)
  g += `<text x="${X(1550)}" y="${Y(1550) - 12}" fill="${C.emersion}" font-family="${FONT}" font-size="11.5" text-anchor="end">emersion: N connections</text>`
  g += `<text x="${M.l + 12}" y="${M.t + 34}" fill="${C.emap}" font-family="${FONT}" font-size="11.5" text-anchor="start">emap-go: 1 connection — flat along the bottom</text>`
  g += legend([{ label: 'emap-go (pooled)', color: C.emap }, { label: 'emersion (one client per task)', color: C.emersion }])
  return wrap(g)
}
function memory() {
  const X = v => lin(v, 0, 2000, M.l, M.l + PW), Y = v => lin(v, 0, 55, M.t + PH, M.t)
  let g = frame('Resident memory vs. number of task-watchers', 'Same workload. Process RSS after GC. Lower is better.')
  for (const t of [0, 10, 20, 30, 40, 50]) {
    g += `<line x1="${M.l}" y1="${Y(t)}" x2="${M.l + PW}" y2="${Y(t)}" stroke="${C.grid}"/>`
    g += `<text x="${M.l - 10}" y="${Y(t) + 4}" fill="${C.dim}" font-family="${FONT}" font-size="11" text-anchor="end">${t}</text>`
  }
  for (const t of [0, 500, 1000, 1500, 2000]) g += `<text x="${X(t)}" y="${M.t + PH + 20}" fill="${C.dim}" font-family="${FONT}" font-size="11" text-anchor="middle">${t}</text>`
  g += `<text x="${M.l + PW / 2}" y="${H - 38}" fill="${C.sub}" font-family="${FONT}" font-size="12" text-anchor="middle">task-watchers (N)</text>`
  g += `<text transform="translate(20,${M.t + PH / 2}) rotate(-90)" fill="${C.sub}" font-family="${FONT}" font-size="12" text-anchor="middle">resident memory (MB)</text>`
  g += poly(emersionRss.map(([x, y]) => [X(x), Y(y)]), C.emersion, C.emersionFill)
  g += poly(emapRss.map(([x, y]) => [X(x), Y(y)]), C.emap, C.emapFill)
  const eM = emersionRss.at(-1)[1].toFixed(0), aM = emapRss.at(-1)[1].toFixed(1)
  g += `<text x="${X(2000) - 6}" y="${Y(emersionRss.at(-1)[1]) + 4}" fill="${C.emersion}" font-family="${FONT}" font-size="11.5" text-anchor="end">emersion: ~${eM} MB</text>`
  g += `<text x="${X(2000) - 6}" y="${Y(emapRss.at(-1)[1]) - 10}" fill="${C.emap}" font-family="${FONT}" font-size="11.5" text-anchor="end">emap-go: ~${aM} MB</text>`
  g += legend([{ label: 'emap-go (pooled)', color: C.emap }, { label: 'emersion (one client per task)', color: C.emersion }])
  return wrap(g)
}
function startupChart() {
  const X = v => lin(log(v), log(1000), log(1000000), M.l, M.l + PW)
  const Y = v => lin(log(v), log(0.3), log(5000), M.t + PH, M.t)
  let g = frame('Time to be ready for new mail vs. inbox backlog',
    'Connect → ready. Log–log. emap-go skips backlog via UIDNEXT; emersion does a naive mailbox sync.')
  for (const t of [1, 10, 100, 1000]) {
    g += `<line x1="${M.l}" y1="${Y(t)}" x2="${M.l + PW}" y2="${Y(t)}" stroke="${C.grid}"/>`
    g += `<text x="${M.l - 10}" y="${Y(t) + 4}" fill="${C.dim}" font-family="${FONT}" font-size="11" text-anchor="end">${t}</text>`
  }
  const lab = { 1000: '1k', 10000: '10k', 100000: '100k', 1000000: '1M' }
  for (const t of [1000, 10000, 100000, 1000000]) {
    g += `<line x1="${X(t)}" y1="${M.t}" x2="${X(t)}" y2="${M.t + PH}" stroke="${C.grid}" stroke-dasharray="2 4"/>`
    g += `<text x="${X(t)}" y="${M.t + PH + 20}" fill="${C.dim}" font-family="${FONT}" font-size="11" text-anchor="middle">${lab[t]}</text>`
  }
  g += `<text x="${M.l + PW / 2}" y="${H - 38}" fill="${C.sub}" font-family="${FONT}" font-size="12" text-anchor="middle">messages already in the inbox (backlog)</text>`
  g += `<text transform="translate(20,${M.t + PH / 2}) rotate(-90)" fill="${C.sub}" font-family="${FONT}" font-size="12" text-anchor="middle">time to ready (ms, log)</text>`
  g += poly(emersionReady.map(([x, y]) => [X(x), Y(y)]), C.emersion, C.emersionFill)
  g += poly(emapReady.map(([x, y]) => [X(x), Y(y)]), C.emap, C.emapFill)
  const mb = (Number(emersion1M.bytes) / 1e6).toFixed(0)
  g += `<text x="${X(1000000) - 6}" y="${Y(Number(emersion1M.ready_ms)) - 10}" fill="${C.emersion}" font-family="${FONT}" font-size="11.5" text-anchor="end">emersion: ${(Number(emersion1M.ready_ms) / 1000).toFixed(1)} s · 1M msgs · ${mb} MB</text>`
  g += `<text x="${X(100000)}" y="${Y(0.7) - 10}" fill="${C.emap}" font-family="${FONT}" font-size="11.5" text-anchor="middle">emap-go: ~1 ms · 0 msgs · 0.5 KB (flat)</text>`
  g += legend([{ label: 'emap-go (UIDNEXT skip)', color: C.emap }, { label: 'emersion (naive mailbox sync)', color: C.emersion }])
  return wrap(g)
}
function deps() {
  let g = frame('Supply-chain footprint',
    'External modules pulled into the build, and go.sum hashes to trust. Lower is better.')
  const groups = [{ label: 'external modules', emap: 0, emersion: 13, max: 14 }, { label: 'go.sum entries', emap: 0, emersion: 37, max: 40 }]
  const gx = [M.l + 150, M.l + 470], barW = 64, gap = 26
  for (let i = 0; i < groups.length; i++) {
    const grp = groups[i], cx = gx[i], base = M.t + PH - 16, scale = (PH - 60) / grp.max
    const eh = Math.max(grp.emap * scale, 2), mh = Math.max(grp.emersion * scale, 2)
    g += `<rect x="${cx - barW - gap / 2}" y="${base - eh}" width="${barW}" height="${eh}" fill="${C.emap}"/>`
    g += `<text x="${cx - barW / 2 - gap / 2}" y="${base - eh - 10}" fill="${C.emap}" font-family="${FONT}" font-size="20" font-weight="700" text-anchor="middle">${grp.emap}</text>`
    g += `<rect x="${cx + gap / 2}" y="${base - mh}" width="${barW}" height="${mh}" fill="${C.emersion}"/>`
    g += `<text x="${cx + barW / 2 + gap / 2}" y="${base - mh - 10}" fill="${C.emersion}" font-family="${FONT}" font-size="20" font-weight="700" text-anchor="middle">${grp.emersion}</text>`
    g += `<text x="${cx}" y="${base + 22}" fill="${C.sub}" font-family="${FONT}" font-size="12.5" text-anchor="middle">${grp.label}</text>`
  }
  g += legend([{ label: 'emap-go (stdlib only, ~1,600 LOC)', color: C.emap }, { label: 'emersion go-imap v2 + imapclient', color: C.emersion }])
  return wrap(g)
}

const charts = {
  'emap-connections.svg': connections(),
  'emap-memory.svg': memory(),
  'emap-startup.svg': startupChart(),
  'emap-deps.svg': deps(),
}
const outDirs = [join(repo, 'charts'), ...process.argv.slice(2)]
for (const [name, svg] of Object.entries(charts)) {
  for (const d of outDirs) writeFileSync(join(d, name), svg)
  console.log('wrote', name, `(${svg.length} B)`)
}
