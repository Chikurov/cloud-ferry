// UI checks for task-7: theme toggle, both themes, dead-code removal, contrast,
// console cleanliness, overflow, and clientToken retry. Drives the preview mock.
import { launch, newPage, stop } from '../design-integration/cdp.mjs';
import { mkdirSync, writeFileSync } from 'node:fs';

const BASE = process.argv[2] || 'http://127.0.0.1:8899';
const EMPTY = process.argv[3] || 'http://127.0.0.1:8898';
const OUT = process.argv[4] || 'docs/design-v3/screens';
const EVID = process.argv[5] || 'docs/design-v3/evidence';
const BASE_DIR = process.argv[6] || '.';
mkdirSync(OUT, { recursive: true });
mkdirSync(EVID, { recursive: true });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const report = [];
const problems = [];
function check(cond, name, detail = '') {
  report.push({ name, ok: !!cond, detail });
  if (!cond) problems.push(`${name}: ${detail}`);
}

const EXPECTED_BG = { light: 'rgb(245, 244, 248)', dark: 'rgb(22, 21, 26)' };
const EXPECTED_META = { light: '#f5f4f8', dark: '#16151a' };

const THEME_SAMPLER = `
  window.__themeSamples = [];
  document.addEventListener('DOMContentLoaded', () => {
    window.__themeSamples.push({
      at: 'DOMContentLoaded',
      theme: document.documentElement.getAttribute('data-theme'),
      colorScheme: getComputedStyle(document.documentElement).colorScheme,
      bg: getComputedStyle(document.body).backgroundColor,
      ready: document.readyState,
    });
  });
  window.addEventListener('load', () => {
    window.__themeSamples.push({ at: 'load', theme: document.documentElement.getAttribute('data-theme') });
  });
`;

const CONTRAST_AUDIT = `(() => {
  const parse = (value) => {
    if (!value) return null;
    let m = String(value).match(/^rgba?\\(([^)]+)\\)$/);
    if (m) {
      const parts = m[1].split(/[,\\s\\/]+/).filter(Boolean).map(Number);
      return { r: parts[0], g: parts[1], b: parts[2], a: parts.length > 3 ? parts[3] : 1 };
    }
    m = String(value).match(/^color\\(srgb\\s+([\\d.]+)\\s+([\\d.]+)\\s+([\\d.]+)(?:\\s*\\/\\s*([\\d.]+))?\\)$/);
    if (m) return { r: Number(m[1]) * 255, g: Number(m[2]) * 255, b: Number(m[3]) * 255, a: m[4] !== undefined ? Number(m[4]) : 1 };
    return null;
  };
  const blend = (top, bottom) => ({
    r: top.r * top.a + bottom.r * (1 - top.a),
    g: top.g * top.a + bottom.g * (1 - top.a),
    b: top.b * top.a + bottom.b * (1 - top.a),
    a: 1,
  });
  const backgroundOf = (element) => {
    if (!element) return null;
    const stack = [];
    let node = element;
    while (node) {
      const cs = getComputedStyle(node);
      if (cs.backgroundImage && cs.backgroundImage !== 'none') return null;
      const bg = parse(cs.backgroundColor);
      if (bg && bg.a > 0) {
        stack.push(bg);
        if (bg.a >= 0.999) break;
      }
      node = node.parentElement;
    }
    if (!stack.length) return null;
    let result = stack.pop();
    if (result.a < 1) result = blend(result, { r: 255, g: 255, b: 255, a: 1 });
    while (stack.length) result = blend(stack.pop(), result);
    return result;
  };
  const lum = (c) => {
    const f = (v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); };
    return 0.2126 * f(c.r) + 0.7152 * f(c.g) + 0.0722 * f(c.b);
  };
  const contrast = (a, b) => {
    const l1 = lum(a); const l2 = lum(b);
    return (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
  };
  const rgbText = (c) => 'rgb(' + Math.round(c.r) + ', ' + Math.round(c.g) + ', ' + Math.round(c.b) + ')';
  const same = (a, b) => Math.abs(a.r - b.r) < 6 && Math.abs(a.g - b.g) < 6 && Math.abs(a.b - b.b) < 6;
  const visible = (el) => {
    if (!el.isConnected) return false;
    const cs = getComputedStyle(el);
    if (cs.display === 'none' || cs.visibility === 'hidden') return false;
    if (parseFloat(cs.opacity) < 0.999) return false;
    const rect = el.getBoundingClientRect();
    if (rect.width < 2 || rect.height < 2) return false;
    if (el.closest('[aria-hidden="true"]')) return false;
    if (el.closest('[hidden]')) return false;
    return true;
  };
  const ownText = (el) => Array.from(el.childNodes).some((n) => n.nodeType === 3 && n.textContent.trim());
  const texts = [];
  for (const el of document.querySelectorAll('body *')) {
    if (!ownText(el) || !visible(el)) continue;
    const cs = getComputedStyle(el);
    const fg = parse(cs.color);
    const bg = backgroundOf(el);
    if (!fg || !bg) continue;
    const ratio = contrast(fg, bg);
    if (ratio < 4.5) {
      texts.push({
        tag: el.tagName.toLowerCase(), cls: String(el.className || '').slice(0, 70),
        text: Array.from(el.childNodes).filter((n) => n.nodeType === 3).map((n) => n.textContent.trim()).join(' ').slice(0, 70),
        fg: rgbText(fg), bg: rgbText(bg), ratio: Math.round(ratio * 100) / 100,
      });
    }
  }
  // Mandatory: the border is the only thing that identifies the control
  // (inputs, secondary buttons, checkboxes). Decorative: the control is
  // identified by its text/icon, so WCAG 1.4.11 does not require the hairline.
  const borders = [];
  const decorativeBorders = [];
  for (const el of document.querySelectorAll('button, input, select, textarea, a[href], [role="switch"], .folder-check-mark')) {
    if (!visible(el)) continue;
    if (el.disabled || el.getAttribute('aria-disabled') === 'true' || el.closest('.is-disabled')) continue;
    const cs = getComputedStyle(el);
    const widths = [cs.borderTopWidth, cs.borderRightWidth, cs.borderBottomWidth, cs.borderLeftWidth].map(parseFloat);
    if (!Math.max(...widths)) continue;
    const bc = parse(cs.borderTopColor);
    if (!bc || bc.a === 0) continue;
    const outer = backgroundOf(el.parentElement) || backgroundOf(el);
    if (!outer) continue;
    const solid = bc.a >= 0.999 ? bc : blend(bc, outer);
    if (same(solid, outer)) continue;
    const ratio = contrast(solid, outer);
    if (ratio >= 3) continue;
    const record = {
      tag: el.tagName.toLowerCase(), cls: String(el.className || '').slice(0, 70),
      border: rgbText(solid), bg: rgbText(outer), ratio: Math.round(ratio * 100) / 100,
    };
    const mandatory = /^(input|select|textarea)$/.test(el.tagName.toLowerCase())
      || el.matches('.btn--secondary, .btn--danger, .folder-check-mark, .switch-track')
      || el.matches('[role="switch"]');
    if (mandatory) borders.push(record);
    else decorativeBorders.push(record);
  }
  const icons = [];
  for (const svg of document.querySelectorAll('svg.icon, .theme-icon')) {
    if (!visible(svg)) continue;
    const cs = getComputedStyle(svg);
    const fg = parse(cs.color);
    const bg = backgroundOf(svg.parentElement) || backgroundOf(svg);
    if (!fg || !bg) continue;
    const ratio = contrast(fg, bg);
    if (ratio < 3) {
      icons.push({ cls: String(svg.getAttribute('class')), fg: rgbText(fg), bg: rgbText(bg), ratio: Math.round(ratio * 100) / 100 });
    }
  }
  return { texts, borders, decorativeBorders, icons };
})()`;

async function pageState(page) {
  return page.evaluate(`(() => ({
    theme: document.documentElement.getAttribute('data-theme'),
    colorScheme: getComputedStyle(document.documentElement).colorScheme,
    bg: getComputedStyle(document.body).backgroundColor,
    ink: getComputedStyle(document.body).color,
    meta: (document.querySelector('meta[name="theme-color"]') || {}).content || '',
    togglesVisible: Array.from(document.querySelectorAll('.js-theme-toggle')).filter((b) => b.getClientRects().length > 0).length,
    presses: Array.from(document.querySelectorAll('.js-theme-toggle')).filter((b) => b.getClientRects().length > 0).map((b) => b.getAttribute('aria-pressed')),
    labels: Array.from(document.querySelectorAll('.js-theme-toggle')).filter((b) => b.getClientRects().length > 0).map((b) => b.getAttribute('aria-label')),
    moon: Array.from(document.querySelectorAll('.theme-icon--moon')).filter((el) => el.getClientRects().length > 0).map((el) => getComputedStyle(el).display),
    sun: Array.from(document.querySelectorAll('.theme-icon--sun')).filter((el) => el.getClientRects().length > 0).map((el) => getComputedStyle(el).display),
    scrollWidth: document.documentElement.scrollWidth,
    bodyScrollWidth: document.body.scrollWidth,
    innerWidth: window.innerWidth,
    samples: window.__themeSamples || [],
    stepPos: !!document.querySelector('#flow-step-pos'),
    setupSteps: !!document.querySelector('#setup-steps'),
    fdest: !!document.querySelector('#f-dest'),
    iz3: /из 3/.test(document.body.innerText),
    setupTitle: (document.querySelector('#setup-body .setup-title') || {}).textContent || '',
  }))()`);
}

function consoleProblems(page) {
  const errs = page.consoleEntries.filter((e) => e.type === 'error' || e.type === 'assert');
  const logs = page.logEntries.filter((e) => e.level === 'error');
  return {
    errors: [...errs.map((e) => e.text), ...logs.map((e) => e.text)],
    exceptions: page.exceptions.map((e) => e.description || e.text),
    http: page.httpFailures.map((f) => `${f.status || f.error} ${f.url}`),
  };
}

async function ready(page, url, waitMs = 2200) {
  await page.navigate(url, { waitMs });
}

async function clearThemeStorage(page) {
  await page.evaluate(`(() => { localStorage.removeItem('cloudferry-theme'); return true; })()`);
}

async function audit(page, label) {
  const a = await page.evaluate(CONTRAST_AUDIT);
  check(a.texts.length === 0, `${label}: text contrast >= 4.5`, JSON.stringify(a.texts));
  check(a.borders.length === 0, `${label}: control border contrast >= 3`, JSON.stringify(a.borders));
  check(a.icons.length === 0, `${label}: icon contrast >= 3`, JSON.stringify(a.icons));
  if (a.decorativeBorders.length) {
    report.push({ name: `${label}: decorative hairline borders (WCAG 1.4.11 exception)`, ok: true, detail: JSON.stringify(a.decorativeBorders) });
  }
  return a;
}

async function openFlow(page) {
  await page.evaluate(`(() => { const b = document.querySelector('#flow-start'); if (b) b.click(); return true; })()`);
  await sleep(1800);
}

async function closeDialog(page, id) {
  await page.evaluate(`(() => { const d = document.getElementById('${id}'); if (d && d.open) d.close(); return true; })()`);
  await sleep(500);
}

async function runHomeChecks(page, width, theme, shots) {
  const st = await pageState(page);
  check(st.theme === theme, `${theme}@${width}: data-theme=${theme}`, JSON.stringify(st));
  check(st.colorScheme === theme, `${theme}@${width}: color-scheme=${theme}`, st.colorScheme);
  check(st.bg === EXPECTED_BG[theme], `${theme}@${width}: body background`, `${st.bg} vs ${EXPECTED_BG[theme]}`);
  check(st.meta === EXPECTED_META[theme], `${theme}@${width}: meta theme-color`, st.meta);
  check(st.togglesVisible >= 1 && st.presses.every((v) => v === String(theme === 'dark')), `${theme}@${width}: aria-pressed`, JSON.stringify(st.presses));
  check(st.labels.every((l) => l === 'Тёмная тема'), `${theme}@${width}: aria-label`, JSON.stringify(st.labels));
  const iconOk = theme === 'dark'
    ? st.sun.every((d) => d !== 'none') && st.moon.every((d) => d === 'none')
    : st.moon.every((d) => d !== 'none') && st.sun.every((d) => d === 'none');
  check(iconOk, `${theme}@${width}: sun/moon icon visibility`, `moon=${st.moon} sun=${st.sun}`);
  check(st.scrollWidth === st.innerWidth && st.bodyScrollWidth === st.innerWidth, `${theme}@${width}: no horizontal overflow`, `${st.scrollWidth}/${st.bodyScrollWidth}/${st.innerWidth}`);
  const sample = st.samples.find((s) => s.at === 'DOMContentLoaded');
  check(!!sample && sample.theme === theme, `${theme}@${width}: theme set before DOMContentLoaded`, JSON.stringify(st.samples));
  check(!!sample && sample.bg === EXPECTED_BG[theme], `${theme}@${width}: first styled paint in theme`, sample && sample.bg);
  check(!st.stepPos, `${theme}@${width}: flow step counter removed`, '');
  check(!st.fdest, `${theme}@${width}: wizard destination field removed`, '');
  await shots.screenshot(`${OUT}/theme-v3-home-${theme}-${width}.png`);
  const homeAudit = await audit(page, `${theme}@${width} home`);
  await openFlow(page);
  const flowState = await pageState(page);
  check(flowState.scrollWidth === flowState.innerWidth, `${theme}@${width}: flow no horizontal overflow`, `${flowState.scrollWidth}/${flowState.innerWidth}`);
  check(!(await page.evaluate(`document.body.innerText.includes('из 3')`)), `${theme}@${width}: no "N из 3" text`, '');
  await shots.screenshot(`${OUT}/theme-v3-flow-${theme}-${width}.png`);
  const flowAudit = await audit(page, `${theme}@${width} flow`);
  // Pick one cloud folder and move to the review step.
  const picked = await page.evaluate(`(() => {
    const check = document.querySelector('#flow-source-tree .folder-check:not([disabled])');
    if (!check) return false;
    check.click();
    return true;
  })()`);
  check(picked, `${theme}@${width}: source folder selectable`);
  await sleep(800);
  await page.evaluate(`(() => { const b = document.querySelector('#folder-choose'); if (b) b.click(); return true; })()`);
  await sleep(1800);
  await shots.screenshot(`${OUT}/theme-v3-review-${theme}-${width}.png`);
  const reviewAudit = await audit(page, `${theme}@${width} review`);
  await closeDialog(page, 'folder-dialog');
  // Account panel.
  await page.evaluate(`(() => { const b = document.querySelector('#account-chip'); if (b) b.click(); return true; })()`);
  await sleep(900);
  await shots.screenshot(`${OUT}/theme-v3-account-${theme}-${width}.png`);
  const accountAudit = await audit(page, `${theme}@${width} account`);
  await closeDialog(page, 'account-dialog');
  const cons = consoleProblems(page);
  check(cons.errors.length === 0, `${theme}@${width}: no console errors`, JSON.stringify(cons.errors));
  check(cons.exceptions.length === 0, `${theme}@${width}: no uncaught exceptions`, JSON.stringify(cons.exceptions));
  check(cons.http.length === 0, `${theme}@${width}: no failed requests`, JSON.stringify(cons.http));
  return { home: homeAudit, flow: flowAudit, review: reviewAudit, account: accountAudit, console: cons };
}

async function runToggleChecks(page, width, shots) {
  // Start from a light system with no stored preference: theme follows the OS.
  await clearThemeStorage(page);
  await page.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'light' }] });
  await ready(page, BASE + '/');
  let st = await pageState(page);
  check(st.theme === 'light', `toggle@${width}: system light -> light`, st.theme);
  await page.evaluate(`(() => { document.querySelector('.js-theme-toggle').click(); return true; })()`);
  await sleep(300);
  st = await pageState(page);
  const stored = await page.evaluate(`localStorage.getItem('cloudferry-theme')`);
  check(st.theme === 'dark', `toggle@${width}: click switches to dark`, st.theme);
  check(st.presses[0] === 'true', `toggle@${width}: aria-pressed=true after switch`);
  check(stored === 'dark', `toggle@${width}: dark saved to localStorage`, String(stored));
  check(st.bg === EXPECTED_BG.dark, `toggle@${width}: dark background after switch`, st.bg);
  // Reload under the opposite system preference: the saved choice must win.
  await page.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'light' }] });
  await ready(page, BASE + '/');
  st = await pageState(page);
  check(st.theme === 'dark', `toggle@${width}: saved dark survives reload under light OS`, st.theme);
  const sample = st.samples.find((s) => s.at === 'DOMContentLoaded');
  check(!!sample && sample.theme === 'dark' && sample.bg === EXPECTED_BG.dark, `toggle@${width}: no flash on reload`, JSON.stringify(st.samples));
  await page.evaluate(`(() => { document.querySelector('.js-theme-toggle').click(); return true; })()`);
  await sleep(300);
  st = await pageState(page);
  check(st.theme === 'light', `toggle@${width}: second click returns to light`, st.theme);
  check(st.presses[0] === 'false', `toggle@${width}: aria-pressed=false after return`);
  await shots.screenshot(`${OUT}/theme-v3-toggled-light-${width}.png`);
}

async function runSetupChecks(page, width, theme, shots) {
  await page.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: theme }] });
  await ready(page, EMPTY + '/#/setup');
  let st = await pageState(page);
  check(st.theme === theme, `setup ${theme}@${width}: theme applied`, st.theme);
  check(st.togglesVisible === 1, `setup ${theme}@${width}: theme toggle in setup header`, String(st.togglesVisible));
  check(!st.setupSteps, `setup ${theme}@${width}: step strips removed`);
  check(st.setupTitle === 'Подключение', `setup ${theme}@${width}: single-step title`, st.setupTitle);
  check(!st.fdest, `setup ${theme}@${width}: dead destination input removed`);
  check(st.scrollWidth === st.innerWidth, `setup ${theme}@${width}: no horizontal overflow`, `${st.scrollWidth}/${st.innerWidth}`);
  await shots.screenshot(`${OUT}/theme-v3-setup-${theme}-${width}.png`);
  await audit(page, `setup ${theme}@${width}`);
  const cons = consoleProblems(page);
  check(cons.errors.length === 0, `setup ${theme}@${width}: no console errors`, JSON.stringify(cons.errors));
  check(cons.exceptions.length === 0, `setup ${theme}@${width}: no exceptions`, JSON.stringify(cons.exceptions));
}

async function runClientTokenChecks(page, width) {
  const HOOK = `
    window.__cfJobPosts = [];
    window.__cfFailNextJob = true;
    const realFetch = window.fetch;
    window.fetch = function (input, init) {
      const url = typeof input === 'string' ? input : (input && input.url) || '';
      const isCreate = /\\/api\\/jobs$/.test(url) && init && init.method === 'POST';
      if (isCreate) {
        try { window.__cfJobPosts.push(JSON.parse(init.body)); } catch (e) { window.__cfJobPosts.push({ parseError: String(e) }); }
        if (window.__cfFailNextJob) {
          window.__cfFailNextJob = false;
          return Promise.reject(new TypeError('Failed to fetch'));
        }
      }
      return realFetch.apply(this, arguments);
    };
  `;
  await page.send('Page.addScriptToEvaluateOnNewDocument', { source: HOOK });
  await page.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'light' }] });
  await ready(page, BASE + '/');
  await clearThemeStorage(page);
  await ready(page, BASE + '/');
  await page.evaluate(`(() => { const b = document.querySelector('#flow-start'); if (b) b.click(); return true; })()`);
  await sleep(1600);
  await page.evaluate(`(() => { const c = document.querySelector('#flow-source-tree .folder-check:not([disabled])'); if (c) c.click(); return true; })()`);
  await sleep(400);
  await page.evaluate(`(() => { const b = document.querySelector('#folder-choose'); if (b) b.click(); return true; })()`);
  await sleep(1800);
  await page.evaluate(`(() => { const b = document.querySelector('#folder-choose'); if (b) b.click(); return true; })()`);
  await sleep(4500);
  const result = await page.evaluate(`(() => ({
    posts: window.__cfJobPosts || [],
    dialogOpen: !!(document.getElementById('folder-dialog') && document.getElementById('folder-dialog').open),
    toasts: Array.from(document.querySelectorAll('#toasts .toast')).map((t) => t.innerText.replace(/\\s+/g, ' ').slice(0, 120)),
    nowText: (document.querySelector('#cell-now') || {}).innerText || '',
  }))()`);
  check(result.posts.length === 2, `clientToken@${width}: first attempt failed, retry sent`, `posts=${result.posts.length}`);
  const first = result.posts[0] || {};
  const second = result.posts[1] || {};
  check(typeof first.clientToken === 'string' && first.clientToken.length >= 8, `clientToken@${width}: first POST carries clientToken`, JSON.stringify(first.clientToken));
  check(second.clientToken === first.clientToken, `clientToken@${width}: retry reuses the same token`, `${first.clientToken} vs ${second.clientToken}`);
  check(JSON.stringify(second.sourcePaths) === JSON.stringify(first.sourcePaths), `clientToken@${width}: retry body unchanged`);
  check(!result.dialogOpen, `clientToken@${width}: dialog closed after confirmed start`);
  check(/Скачивание началось/.test(result.toasts.join(' ')) || /Скачивание началось/.test(result.nowText), `clientToken@${width}: success toast after retry`, JSON.stringify(result.toasts));
  const cons = consoleProblems(page);
  check(cons.errors.length === 0 && cons.exceptions.length === 0, `clientToken@${width}: console clean`, JSON.stringify(cons));
  return result;
}

async function main() {
  const browser = await launch({ port: 9337, width: 1440, height: 900 });
  const pages = [];
  try {
    for (const width of [390, 1440]) {
      const height = width === 390 ? 844 : 900;
      for (const theme of ['light', 'dark']) {
        const page = await newPage(browser.base, 'about:blank', width, height);
        pages.push(page);
        await page.send('Page.addScriptToEvaluateOnNewDocument', { source: THEME_SAMPLER });
        await page.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: theme }] });
        await ready(page, BASE + '/');
        await clearThemeStorage(page);
        await ready(page, BASE + '/');
        const res = await runHomeChecks(page, width, theme, page);
        writeFileSync(`${EVID}/theme-cleanup-${theme}-${width}-audit.json`, JSON.stringify(res, null, 2));
        await runToggleChecks(page, width, page);
        await page.close();
      }
      // Setup (no account) on the empty mock: both widths, both themes.
      for (const theme of ['light', 'dark']) {
        const page = await newPage(browser.base, 'about:blank', width, height);
        pages.push(page);
        await page.send('Page.addScriptToEvaluateOnNewDocument', { source: THEME_SAMPLER });
        await runSetupChecks(page, width, theme, page);
        await page.close();
      }
    }
    // clientToken end-to-end against the normal mock.
    const clientPage = await newPage(browser.base, 'about:blank', 1440, 900);
    pages.push(clientPage);
    const tokenResult = await runClientTokenChecks(clientPage, 1440);
    writeFileSync(`${EVID}/theme-cleanup-client-token.json`, JSON.stringify(tokenResult, null, 2));
    await clientPage.close();
  } finally {
    await stop(browser);
  }
  const summary = { base: BASE, empty: EMPTY, problems, report };
  writeFileSync(`${EVID}/theme-cleanup-report.json`, JSON.stringify(summary, null, 2));
  console.log(JSON.stringify({ checks: report.length, failed: problems.length, problems }, null, 2));
  if (problems.length) process.exitCode = 1;
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
