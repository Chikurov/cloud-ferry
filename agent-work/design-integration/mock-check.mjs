// Mock-API check: the UI must render exactly what the mock returns and must not
// invent values. Normal mock has one account/job; MOCK_EMPTY has none.
import { launch, newPage, stop } from './cdp.mjs';
import { mkdirSync, writeFileSync } from 'node:fs';

const FULL = process.argv[2] || 'http://127.0.0.1:8899';
const EMPTY = process.argv[3] || 'http://127.0.0.1:8898';
const OUT = process.argv[4] || '/tmp/cf-test/shots';
mkdirSync(OUT, { recursive: true });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const report = [];
const problems = [];
function check(cond, name, detail = '') {
  report.push({ name, ok: !!cond, detail });
  if (!cond) problems.push(`${name}: ${detail}`);
}

async function consoleState(page, label) {
  const errors = page.consoleEntries.filter((e) => e.type === 'error');
  const exceptions = page.exceptions;
  check(errors.length === 0, `mock ${label}: no console.error`, JSON.stringify(errors));
  check(exceptions.length === 0, `mock ${label}: no uncaught exceptions`, JSON.stringify(exceptions));
}

async function main() {
  const browser = await launch({ port: 9337, width: 1440, height: 900 });
  const page = await newPage(browser.base, 'about:blank', 1440, 900);
  try {
    // --- full mock: values must come from the API ---
    await page.navigate(FULL + '/', { waitMs: 2600 });
    const api = await page.evaluate(`Promise.all([
      fetch('/api/state').then((r) => r.json()),
      fetch('/api/system').then((r) => r.json()),
    ])`);
    const [state, system] = api;
    const job = state.jobs[0];
    const home = await page.evaluate(`(() => {
      const row = document.querySelector('#job-list .job-row');
      return {
        drive: document.querySelector('.drive-row .drive-name h2') ? document.querySelector('.drive-row .drive-name h2').textContent : null,
        jobCount: document.querySelectorAll('#job-list .job-row').length,
        row: row ? {
          status: row.dataset.status,
          title: row.querySelector('h2').textContent,
          stats: Array.from(row.querySelectorAll('.task-stats span')).map((s) => s.textContent),
          progress: row.querySelector('progress').value,
        } : null,
        body: document.querySelector('#app-view').innerText,
      };
    })()`);
    check(home.jobCount === 1, 'mock full: one job from /api/state', JSON.stringify(home));
    check(home.drive === 'artem', 'mock full: account login from /api/state', String(home.drive));
    // 13212057600 / 31138512896 -> «12,3 из 29,0 ГБ»; 8808038 -> «8,4 МБ/с»; eta 21400 -> «5 ч 56 мин»
    const sizeText = home.row ? home.row.stats[0] : '';
    check(/^12,3 из 29,0\u00A0ГБ$/.test(sizeText), 'mock full: job size text derived from job.stats', sizeText);
    check(/8,4\u00A0МБ\/с/.test(home.row ? home.row.stats[1] : ''), 'mock full: job speed from job.stats.speed', home.row && home.row.stats[1]);
    check(/5\u00A0ч 56\u00A0мин/.test(home.row ? home.row.stats[1] : ''), 'mock full: eta from job.stats.eta', home.row && home.row.stats[1]);
    check(home.row && home.row.progress === 42, 'mock full: progress from job.stats bytes/total', String(home.row && home.row.progress));
    check(!/42\s*%/.test(home.body), 'mock full: no invented "42 %" literal in the home text', '');

    await page.evaluate(`document.querySelector('#account-chip').click(); true`);
    await sleep(600);
    const panel = await page.evaluate(`(() => ({
      login: document.querySelector('#account-login').textContent,
      disk: document.querySelector('#account-disk-value').textContent,
      bar: document.querySelector('#account-disk-bar').style.width,
      server: document.querySelector('#account-server-value').textContent,
      status: document.querySelector('#account-status').textContent.trim(),
    }))()`);
    check(panel.login === 'artem', 'mock full: panel login from account.login', JSON.stringify(panel));
    check(panel.disk === '128,5\u00A0из 200\u00A0ГБ', 'mock full: disk usage from /api/system cloud', panel.disk);
    check(panel.bar === '64%', 'mock full: disk bar from system cloud used/total', panel.bar);
    check(panel.server === '931,5\u00A0ГБ из 1,8\u00A0ТБ', 'mock full: server space from /api/system roots', panel.server);
    check(/Доступно/.test(panel.status), 'mock full: access from account.status', panel.status);
    await page.screenshot(`${OUT}/mock-account-1440.png`);
    await page.evaluate(`document.querySelector('#account-dialog [data-dialog-close]').click(); true`);
    await sleep(400);

    // flow on mock: cloud tree must come from /api/accounts/a1/folders
    await page.evaluate(`document.querySelector('#flow-start').click(); true`);
    await sleep(1800);
    const flow = await page.evaluate(`(() => ({
      rows: Array.from(document.querySelectorAll('#flow-source-tree .folder-check')).map((el) => el.dataset.path),
      dialogOpen: document.querySelector('#folder-dialog').open,
      context: document.querySelector('#folder-dialog').dataset.context,
    }))()`);
    check(flow.dialogOpen && flow.context === 'flow-source', 'mock full: download flow opens', JSON.stringify(flow));
    check(flow.rows.join(',') === ['Фотографии', 'Документы', 'Видео'].join(','),
      'mock full: cloud folders from mock /api/accounts/a1/folders', JSON.stringify(flow.rows));
    await page.screenshot(`${OUT}/mock-flow-1440.png`);
    await page.evaluate(`document.querySelector('#folder-dialog [data-dialog-close]').click(); true`);
    await sleep(500);
    await consoleState(page, 'full');

    // --- empty mock: no invented data at all ---
    const emptyPage = await newPage(browser.base, 'about:blank', 1440, 900);
    await emptyPage.navigate(EMPTY + '/', { waitMs: 2600 });
    const empty = await emptyPage.evaluate(`(() => {
      const text = document.querySelector('#app-view').innerText + ' ' + document.querySelector('#setup-view').innerText;
      return {
        appHidden: document.querySelector('#app-view').hidden,
        setupHidden: document.querySelector('#setup-view').hidden,
        plus: !!document.querySelector('.plus-tile'),
        text,
      };
    })()`);
    check(!empty.appHidden && empty.setupHidden && empty.plus, 'mock empty: empty state with connect tile', JSON.stringify(empty));
    check(!/\d/.test(empty.text.replace(/\s+/g, ' ')), 'mock empty: no numbers rendered in empty state', empty.text.slice(0, 400));
    check(!/ГБ|МБ|%/.test(empty.text), 'mock empty: no size or percent units in empty state', empty.text.slice(0, 400));
    await emptyPage.screenshot(`${OUT}/mock-empty-1440.png`);
    await consoleState(emptyPage, 'empty');
    const overflow = await emptyPage.evaluate(`({ scroll: document.documentElement.scrollWidth, inner: window.innerWidth })`);
    check(overflow.scroll === overflow.inner, 'mock empty: no horizontal overflow', JSON.stringify(overflow));
    await emptyPage.close();

    const result = { problems, report, api: page.apiResponses.map((r) => `${r.status} ${new URL(r.url).pathname}`) };
    writeFileSync(`${OUT}/report-mock.json`, JSON.stringify(result, null, 2));
    console.log(JSON.stringify({ ok: problems.length === 0, problems, checks: report.length }, null, 2));
    for (const p of problems) console.log('PROBLEM:', p);
    process.exitCode = problems.length ? 1 : 0;
  } finally {
    await page.close();
    await stop(browser);
  }
}

main().catch((error) => { console.error('FATAL', error); process.exit(2); });
