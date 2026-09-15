// Screen recording of the Atlas Console, cut to the speech durations in timing.json
// (tts.py measured them). Every scene gets exactly as long as its narration takes —
// so editing the narration moves the picture with it.
//
// What is recorded is what a person would see: real clicks, real scrolling, and a
// visible mouse pointer — which Playwright does not draw into the video itself, so
// ACTOR attaches one to the page as a DOM element.
import { createRequire } from 'node:module';
import { readFileSync } from 'node:fs';
const require = createRequire(import.meta.url);
// Playwright is a devDependency of e2e/, not of this directory: the recording rides
// the same browser the end-to-end tests already bring.
const { chromium } = require('../../e2e/node_modules/@playwright/test');

const BUILD = process.argv[2] || './build';
const BASE  = process.env.ATLAS_BASE || 'http://127.0.0.1:8080';
const USER  = process.env.ATLAS_USER || 'admin';
const PASS  = process.env.ATLAS_PASS || 'atlas-demo-2026';
const W = 1440, H = 810;
const LEAD = 3000;   // a fixed lead-in; mux.sh trims exactly this much

const timing = JSON.parse(readFileSync(`${BUILD}/timing.json`, 'utf8'));

const ACTOR = `
(() => {
  const CSS = \`
    #__cur{position:fixed;z-index:2147483647;width:22px;height:22px;margin:-2px 0 0 -2px;
      pointer-events:none;left:0;top:0;transition:transform .05s linear}
    #__ring{position:fixed;z-index:2147483646;width:38px;height:38px;margin:-19px 0 0 -19px;
      border-radius:50%;border:2px solid #2563eb;background:rgba(37,99,235,.18);pointer-events:none;opacity:0}
    #__ring.go{animation:__ping .55s ease-out}
    @keyframes __ping{0%{opacity:.9;transform:scale(.35)}100%{opacity:0;transform:scale(1.2)}}
    #__cap{position:fixed;left:50%;bottom:32px;transform:translateX(-50%);z-index:2147483645;
      max-width:1120px;pointer-events:none;background:rgba(15,23,42,.94);color:#fff;border-radius:12px;
      padding:13px 24px;font:500 19px/1.4 system-ui,-apple-system,"Segoe UI",sans-serif;text-align:center;
      box-shadow:0 12px 34px rgba(0,0,0,.3);opacity:0;transition:opacity .3s ease}
    #__cap.on{opacity:1}
    #__cap b{color:#93c5fd;font-weight:600}
    #__card{position:fixed;inset:0;z-index:2147483644;pointer-events:none;background:#0f172a;color:#fff;
      display:flex;flex-direction:column;align-items:center;justify-content:center;gap:16px;
      font-family:system-ui,-apple-system,"Segoe UI",sans-serif;opacity:0;transition:opacity .45s ease}
    #__card.on{opacity:1}
    #__card .m{width:74px;height:74px;border-radius:18px;background:#fff;color:#0f172a;display:grid;
      place-items:center;font-size:40px;font-weight:800}
    #__card .t{font-size:47px;font-weight:700;letter-spacing:-.02em}
    #__card .s{font-size:21px;color:#94a3b8;max-width:860px;text-align:center;line-height:1.45}
  \`;
  // Mount only once there is a <body>: addInitScript runs ahead of the parser, and
  // anything hung off documentElement before that gets cleared away again.
  function mount() {
    if (document.getElementById('__cur')) return true;
    if (!document.body) return false;
    const st = document.createElement('style'); st.textContent = CSS;
    (document.head || document.body).appendChild(st);
    const cur = document.createElement('div'); cur.id = '__cur';
    cur.innerHTML = '<svg width="22" height="22" viewBox="0 0 22 22"><path d="M3 2 L3 17.5 L7.2 13.6 L9.9 19.6 L12.6 18.3 L10 12.4 L15.8 12.2 Z" fill="#fff" stroke="#0f172a" stroke-width="1.4" stroke-linejoin="round"/></svg>';
    const ring = document.createElement('div'); ring.id = '__ring';
    const cap  = document.createElement('div'); cap.id = '__cap';
    const card = document.createElement('div'); card.id = '__card';
    card.innerHTML = '<div class="m">A</div><div class="t"></div><div class="s"></div>';
    document.body.append(cur, ring, cap, card);
    document.addEventListener('mousemove', e => {
      cur.style.transform = 'translate(' + e.clientX + 'px,' + e.clientY + 'px)';
    }, true);
    document.addEventListener('mousedown', e => {
      ring.style.left = e.clientX + 'px'; ring.style.top = e.clientY + 'px';
      ring.classList.remove('go'); void ring.offsetWidth; ring.classList.add('go');
    }, true);
    return true;
  }
  if (!mount()) {
    document.addEventListener('DOMContentLoaded', mount);
    const iv = setInterval(() => { if (mount()) clearInterval(iv); }, 40);
    setTimeout(() => clearInterval(iv), 15000);
  }
  window.__say = h => { const c = document.getElementById('__cap'); if (!c) return false;
    if (!h) { c.classList.remove('on'); return true; } c.innerHTML = h; c.classList.add('on'); return true; };
  window.__card = (t, s) => { const c = document.getElementById('__card'); if (!c) return false;
    if (t === null) { c.classList.remove('on'); return true; }
    c.querySelector('.t').textContent = t; c.querySelector('.s').textContent = s || '';
    c.classList.add('on'); return true; };
})();`;

const sleep = ms => new Promise(r => setTimeout(r, ms));

async function main() {
  const browser = await chromium.launch({ args: ['--hide-scrollbars', '--force-device-scale-factor=1'] });
  const ctx = await browser.newContext({
    viewport: { width: W, height: H },
    recordVideo: { dir: `${BUILD}/raw`, size: { width: W, height: H } },
    locale: 'de-CH',
  });
  await ctx.addInitScript(ACTOR);
  const page = await ctx.newPage();
  const recStart = Date.now();

  const glide = async (x, y, steps = 24) => page.mouse.move(x, y, { steps });
  const boxOf = async sel => {
    const el = page.locator(sel).first();
    await el.scrollIntoViewIfNeeded().catch(() => {});
    return el.boundingBox();
  };
  const point = async (sel, pause = 340) => {
    const b = await boxOf(sel); if (!b) return false;
    await glide(b.x + b.width / 2, b.y + b.height / 2); await sleep(pause); return true;
  };
  const click = async (sel, pause = 340) => {
    if (!await point(sel, pause)) return false;
    await page.mouse.down(); await sleep(70); await page.mouse.up(); return true;
  };
  const say  = async h => { if (!await page.evaluate(x => window.__say ? window.__say(x) : false, h))
      console.warn('  caption not set:', String(h).slice(0, 40)); };
  const card = async (t, s) => { if (!await page.evaluate(([a, b]) => window.__card ? window.__card(a, b) : false, [t, s]))
      console.warn('  title card not set'); };
  const scroll = async (px, ms) => {
    const steps = Math.max(10, Math.round(ms / 55));
    for (let i = 0; i < steps; i++) { await page.mouse.wheel(0, px / steps); await sleep(ms / steps); }
  };
  /** Navigate by the tab strip — and check that it actually happened. An open app
   *  switcher lays a #scrim over the page that swallows the first click; the second
   *  one lands. */
  const navLink = label => page.locator('#topnav a').filter({ hasText: new RegExp('^' + label + '$') }).first();
  const nav = async label => {
    const want = await navLink(label).getAttribute('href');
    for (let i = 0; i < 3; i++) {
      const b = await navLink(label).boundingBox();
      if (b) {
        await glide(b.x + b.width / 2, b.y + b.height / 2); await sleep(260);
        await page.mouse.down(); await sleep(70); await page.mouse.up();
      }
      await sleep(520);
      if (await page.evaluate(() => location.hash) === want) return true;
    }
    console.warn('  navigation to', label, 'not confirmed');
    return false;
  };
  /** Bring the element up under the tab strip — in steps, not in one jump. Through a
   *  locator, so Playwright selectors such as :has-text() work here too. */
  const scrollToEl = async (sel, ms, offset = 86) => {
    const box = await page.locator(sel).first().boundingBox().catch(() => null);
    if (!box) { console.warn('  scroll target missing:', sel); return; }
    await scroll(box.y - offset, ms);
  };
  const scrollTop = async ms => { await scroll(-(await page.evaluate(() => window.scrollY)) - 40, ms); };
  const closeDrawer = async () => {
    await click('#drawer-close', 200);
    await page.waitForFunction(() => document.getElementById('drawer')?.hidden !== false,
                               null, { timeout: 4000 }).catch(() => console.warn('  app switcher stayed open'));
  };

  // ---- The scenes. The key is the id from the narration script. -------------------
  const SCENES = {
    async intro(d) { await card('Atlas Console', 'Die Oberfläche, über die ein Atlas-Server betrieben wird');
                     await sleep(Math.max(0, d - 700)); await card(null, ''); },
    async login(d) {
      await glide(W / 2, H / 2 + 60);
      const u = page.locator('#login-form input').first();
      const p = page.locator('#login-form input[type=password]').first();
      await point('#login-form input', 150); await u.click(); await u.type(USER, { delay: 95 });
      await point('#login-form input[type=password]', 150); await p.click(); await p.type(PASS, { delay: 70 });
      await sleep(Math.max(300, d - 5400));
      await click('#login-form button[type=submit]'); await sleep(1500);
    },
    async dashboard() { /* already on screen — just let it be seen */ },
    async 'dashboard-stats'(d) { await scrollToEl('#dep-summary', Math.min(2400, d - 1400), 150); },
    async apps(d) {
      await scrollTop(900);
      await click('#app-switcher'); await sleep(Math.max(600, d - 3000));
      await closeDrawer();
    },
    async engine() { await nav('Engine'); },
    async 'engine-build'(d) { await scrollToEl('#build-card', Math.min(1900, d - 1600)); },
    async workers(d) { await scrollTop(700); await nav('Workers');
                       await scroll(470, Math.min(2400, d - 3400)); },
    async 'ai-access'() { await scrollTop(700); await nav('AI access'); },
    async org() { await nav('Organization'); },
    async 'org-appearance'(d) { await scrollToEl('.card:has(h2:has-text("Appearance"))', Math.min(2000, d - 1400)); },
    async logs() { await scrollTop(700); await nav('Logs'); },
    async audit() { await nav('Audit log'); },
    async backup() { await nav('Backup'); },
    async outro(d) { await say(''); await sleep(250);
                     await card('Atlas Console', 'Dashboard · Engine · Workers · AI access · Organization · Logs · Audit · Backup');
                     await sleep(Math.max(0, d - 250)); },
  };

  await page.goto(BASE + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('#login-form', { timeout: 20000 }).catch(() => {});
  await page.waitForFunction(() => !!document.getElementById('__cap'), null, { timeout: 10000 });
  const wait = LEAD - (Date.now() - recStart); if (wait > 0) await sleep(wait);

  const t0 = Date.now();
  for (const s of timing.scenes) {
    const startedAt = Date.now();
    if (s.caption && s.id !== 'intro' && s.id !== 'outro') await say(s.caption);
    const run = SCENES[s.id];
    if (!run) console.warn('  (no screen action for scene', s.id + ')');
    else await run(s.duration * 1000).catch(e => console.warn('  scene', s.id, 'incomplete:', e.message));
    const rest = s.duration * 1000 - (Date.now() - startedAt);
    if (rest > 0) await sleep(rest);
    console.log(`  ${s.id.padEnd(16)} want ${s.duration.toFixed(1)}s  got ${((Date.now() - startedAt) / 1000).toFixed(1)}s`);
  }
  const played = (Date.now() - t0) / 1000;
  await sleep(600);

  const video = page.video();
  await ctx.close();
  console.log(`\nlead-in ${(LEAD / 1000).toFixed(1)}s, played ${played.toFixed(1)}s`);
  console.log('RAW ' + await video.path());
  await browser.close();
}
main().catch(e => { console.error('FAILED:', e); process.exit(1); });
