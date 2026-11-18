// Theme check: dark and light snapshots of home, flow and the file panel.
// Reports the computed background/text colors of the file panel and checks
// that a visible difference exists between them (readability sanity check).
import { launch, newPage, stop } from './cdp.mjs';
import { mkdirSync, writeFileSync } from 'node:fs';

const BASE = process.argv[2] || 'http://127.0.0.1:8877';
const OUT = process.argv[3] || '/tmp/cf-test/shots';
mkdirSync(OUT, { recursive: true });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const report = [];
const problems = [];
function check(cond, name, detail = '') {
  report.push({ name, ok: !!cond, detail });
  if (!cond) problems.push(`${name}: ${detail}`);
}

function parseRgb(value) {
  const m = String(value).match(/rgba?\(([^)]+)\)/);
  if (!m) return null;
  const nums = m[1].split(',').map((n) => Number(n.trim()));
  return { r: nums[0], g: nums[1], b: nums[2], a: nums.length > 3 ? nums[3] : 1 };
}
function luminance(c) {
  const lin = (v) => {
    const s = v / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * lin(c.r) + 0.7152 * lin(c.g) + 0.0722 * lin(c.b);
}
function contrast(fg, bg) {
  if (!fg || !bg) return 0;
  const l1 = luminance(fg);
  const l2 = luminance(bg);
  return (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
}

async function openFilePanel(page) {
  await page.evaluate(`document.querySelector('#flow-start').click(); true`);
  await sleep(1600);
  const eye = await page.evaluate(`(() => {
    const button = document.querySelector('.folder-files-toggle[data-preview="Фотографии"]');
    if (!button) return false;
    button.click();
    return true;
  })()`);
  await sleep(1500);
  return eye;
}

async function closeFlow(page) {
  await page.evaluate(`(() => { const d = document.querySelector('#folder-dialog'); if (d && d.open) d.close(); return true; })()`);
  await sleep(300);
}

async function main() {
  const browser = await launch({ port: 9339, width: 1440, height: 900 });
  const page = await newPage(browser.base, 'about:blank', 1440, 900);
  try {
    for (const scheme of ['dark', 'light']) {
      await page.send('Emulation.setEmulatedMedia', {
        features: [{ name: 'prefers-color-scheme', value: scheme }],
      });
      await page.navigate(BASE + '/', { waitMs: 2400 });
      const layout = await page.evaluate(`(() => ({
        scheme: matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light',
        scrollWidth: document.documentElement.scrollWidth,
        innerWidth: window.innerWidth,
        bodyBg: getComputedStyle(document.body).backgroundColor,
        ink: getComputedStyle(document.body).color,
      }))()`);
      check(layout.scrollWidth === layout.innerWidth, `${scheme}: no horizontal overflow`, JSON.stringify(layout));
      check(layout.scheme === scheme, `${scheme}: media emulation applied`, layout.scheme);
      await page.screenshot(`${OUT}/theme-${scheme}-home-1440.png`);

      const opened = await openFilePanel(page);
      check(opened, `${scheme}: file panel opened`);
      const colors = await page.evaluate(`(() => {
        const panel = document.querySelector('.files-panel');
        const title = document.querySelector('.files-title');
        const name = document.querySelector('.file-name');
        return {
          panel: getComputedStyle(panel).backgroundColor,
          title: getComputedStyle(title).color,
          name: getComputedStyle(name).color,
        };
      })()`);
      const panelBg = parseRgb(colors.panel);
      const titleFg = parseRgb(colors.title);
      const nameFg = parseRgb(colors.name);
      const titleContrast = contrast(titleFg, panelBg);
      const nameContrast = contrast(nameFg, panelBg);
      check(titleContrast >= 3, `${scheme}: file panel title contrast >= 3:1`, `${titleContrast.toFixed(2)} (${colors.title} on ${colors.panel})`);
      check(nameContrast >= 3, `${scheme}: file name contrast >= 3:1`, `${nameContrast.toFixed(2)} (${colors.name} on ${colors.panel})`);
      await page.screenshot(`${OUT}/theme-${scheme}-files-1440.png`);
      await closeFlow(page);
      const bad4 = page.apiResponses.filter((r) => r.status >= 400);
      check(bad4.length === 0, `${scheme}: no failed API requests`, JSON.stringify(bad4));
    }
    const errors = page.consoleEntries.filter((e) => e.type === 'error');
    check(errors.length === 0, 'theme: no console errors', JSON.stringify(errors));
    writeFileSync(`${OUT}/report-theme.json`, JSON.stringify({ problems, report }, null, 2));
    console.log(JSON.stringify({ ok: problems.length === 0, problems, checks: report.length }, null, 2));
    for (const p of problems) console.log('PROBLEM:', p);
    process.exitCode = problems.length ? 1 : 0;
  } finally {
    await page.close();
    await stop(browser);
  }
}

main().catch((error) => { console.error('FATAL', error); process.exit(2); });
