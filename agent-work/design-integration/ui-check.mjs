// Browser scenario checks for the integrated Cloud Ferry UI.
// Usage: node ui-check.mjs <baseUrl> [phase] [outdir]
import { launch, newPage, stop } from './cdp.mjs';
import { mkdirSync, writeFileSync } from 'node:fs';

const BASE = process.argv[2] || 'http://127.0.0.1:8877';
const PHASE = process.argv[3] || 'home';
const OUT = process.argv[4] || '/tmp/cf-test/shots';
mkdirSync(OUT, { recursive: true });

const report = [];
const problems = [];
function ok(name, detail = '') { report.push({ name, ok: true, detail }); }
function bad(name, detail = '') { report.push({ name, ok: false, detail }); problems.push(`${name}: ${detail}`); }
function check(cond, name, detail = '') { cond ? ok(name, detail) : bad(name, detail); }

function layoutExpr() {
  return `(() => {
    const html = document.documentElement;
    const body = document.body;
    return {
      innerWidth: window.innerWidth,
      innerHeight: window.innerHeight,
      htmlScrollWidth: html.scrollWidth,
      bodyScrollWidth: body.scrollWidth,
      htmlScrollHeight: html.scrollHeight,
      title: document.title,
      appHidden: document.querySelector('#app-view').hidden,
      setupHidden: document.querySelector('#setup-view').hidden,
      bootHidden: document.querySelector('#boot-view') ? document.querySelector('#boot-view').hidden : 'removed',
      bodyText: body.innerText.slice(0, 4000),
    };
  })()`;
}

async function snapshotOverflow(page, label) {
  const layout = await page.evaluate(layoutExpr());
  check(layout.htmlScrollWidth === layout.innerWidth, `overflow ${label}: html scrollWidth == innerWidth`,
    `scrollWidth=${layout.htmlScrollWidth} innerWidth=${layout.innerWidth} bodyScroll=${layout.bodyScrollWidth}`);
  if (layout.bodyScrollWidth > layout.innerWidth) bad(`overflow ${label}: body wider than viewport`, String(layout.bodyScrollWidth));
  return layout;
}

function consoleProblems(page) {
  const errs = page.consoleEntries.filter((e) => e.type === 'error' || e.type === 'assert');
  const exceptions = page.exceptions.slice();
  const logErrors = page.logEntries.filter((e) => e.level === 'error' && e.source === 'javascript');
  return { errs, exceptions, logErrors };
}

async function reportConsole(page, label) {
  const { errs, exceptions, logErrors } = consoleProblems(page);
  const bad4 = page.apiResponses.filter((r) => r.status >= 400);
  check(errs.length === 0, `console ${label}: no console.error`, JSON.stringify(errs));
  check(exceptions.length === 0, `console ${label}: no uncaught exceptions`, JSON.stringify(exceptions));
  check(logErrors.length === 0, `console ${label}: no JS log errors`, JSON.stringify(logErrors));
  if (bad4.length) {
    bad(`console ${label}: no API responses >= 400`, JSON.stringify(bad4.map((r) => `${r.status} ${r.url}`)));
  } else ok(`console ${label}: no API responses >= 400`);
  return { errs, exceptions, logErrors, bad4 };
}

async function openFlow(page) {
  await page.evaluate(`document.querySelector('#flow-start').click(); true`);
  await new Promise((r) => setTimeout(r, 1600));
  return page.evaluate(`(() => {
    const dlg = document.querySelector('#folder-dialog');
    const tree = document.querySelector('#flow-source-tree');
    const rows = Array.from(document.querySelectorAll('#flow-source-tree .folder-check')).map((el) => el.dataset.path);
    const tags = Array.from(document.querySelectorAll('#flow-source-tree .folder-tag')).map((el) => el.textContent.trim());
    const metas = Array.from(document.querySelectorAll('#flow-source-tree .folder-row-meta')).map((el) => el.textContent.trim());
    const primary = document.querySelector('#folder-choose');
    return {
      open: !!dlg && dlg.open,
      context: dlg ? dlg.dataset.context : '',
      step: dlg ? dlg.dataset.step : '',
      title: document.querySelector('#flow-title') ? document.querySelector('#flow-title').textContent : '',
      rows, tags, metas,
      primary: primary ? { text: primary.textContent.trim(), disabled: primary.disabled } : null,
      footerNote: document.querySelector('#flow-foot-note').hidden ? null : document.querySelector('#flow-foot-note-text').textContent,
      bodyText: dlg ? dlg.innerText.slice(0, 2500) : '',
    };
  })()`);
}

async function main() {
  const browser = await launch({ port: 9333, width: 1440, height: 900 });
  const page = await newPage(browser.base, 'about:blank', 1440, 900);
  try {
    await page.navigate(BASE + '/', { waitMs: 2600 });
    const layout1440 = await snapshotOverflow(page, '1440 home');
    await page.screenshot(`${OUT}/home-1440.png`);
    check(!layout1440.appHidden, '1440: app view visible');
    check(layout1440.bootHidden === true, '1440: boot skeleton hidden');
    await reportConsole(page, '1440 home');

    const home = await page.evaluate(`(() => {
      const rows = Array.from(document.querySelectorAll('#job-list .job-row'));
      return {
        mode: document.querySelector('#home-view').className,
        drive: document.querySelector('.drive-row .drive-name h2') ? document.querySelector('.drive-row .drive-name h2').textContent : null,
        jobCount: rows.length,
        jobs: rows.map((row) => ({
          status: row.dataset.status,
          title: row.querySelector('h2') ? row.querySelector('h2').textContent : '',
          stats: Array.from(row.querySelectorAll('.task-stats span')).map((s) => s.textContent),
          action: row.querySelector('button') ? row.querySelector('button').dataset.action : '',
        })),
        nowHidden: document.querySelector('#cell-now').hidden,
        accountWrapVisible: !document.querySelector('#account-wrap').hidden,
      };
    })()`);
    check(home.jobCount >= 3, 'home: seeded jobs rendered', JSON.stringify(home.jobs));
    check(home.jobs.some((j) => j.status === 'running' && j.action === 'stop'), 'home: running job has pause action', JSON.stringify(home.jobs));
    check(home.jobs.some((j) => j.status === 'done' && j.action === 'dismiss-job'), 'home: done job has dismiss action', JSON.stringify(home.jobs));
    check(home.jobs.some((j) => j.status === 'failed' && j.action === 'resume'), 'home: failed job has resume action', JSON.stringify(home.jobs));
    check(home.accountWrapVisible, 'home: account chip visible');
    check(home.drive !== '', 'home: drive row account name rendered', String(home.drive));

    if (PHASE === 'home') {
      // account panel + probe
      await page.evaluate(`document.querySelector('#account-chip').click(); true`);
      await new Promise((r) => setTimeout(r, 500));
      const panel = await page.evaluate(`(() => {
        const dlg = document.querySelector('#account-dialog');
        return {
          open: !!dlg && dlg.open,
          login: document.querySelector('#account-login').textContent,
          method: document.querySelector('#account-method').textContent,
          status: document.querySelector('#account-status').textContent.trim(),
          checked: document.querySelector('#account-checked').textContent,
          disk: document.querySelector('#account-disk-value').textContent,
          server: document.querySelector('#account-server-value').textContent,
          note: document.querySelector('#account-disk-note').hidden ? '' : document.querySelector('#account-disk-note').textContent,
        };
      })()`);
      check(panel.open, 'account: panel opens');
      check(panel.login === 'artem', 'account: login from API', panel.login);
      check(panel.method === 'Пароль приложения', 'account: method label from provider', panel.method);
      check(/Доступно/.test(panel.status), 'account: status from account.status', panel.status);
      check(panel.disk !== '—', 'account: disk usage from /api/system cloud', panel.disk);
      check(panel.server !== '—', 'account: server free space from /api/system roots', panel.server);
      await page.screenshot(`${OUT}/account-1440.png`);
      await page.evaluate(`document.querySelector('#account-probe').click(); true`);
      await new Promise((r) => setTimeout(r, 1800));
      const probe = await page.evaluate(`(() => {
        const node = document.querySelector('#account-probe-result');
        return { text: node.textContent, hidden: node.hidden, cls: node.className };
      })()`);
      check(!probe.hidden && /Доступ есть/.test(probe.text), 'account probe: result from /api/accounts/{id}/probe', JSON.stringify(probe));
      await page.evaluate(`document.querySelector('#account-dialog [data-dialog-close]').click(); true`);
      await new Promise((r) => setTimeout(r, 400));
      await reportConsole(page, '1440 account');
    }

    if (PHASE === 'home') {
      // --- job actions: stop / resume / dismiss ---
      const clickJobAction = async (status, expectedAction) => {
        const clicked = await page.evaluate(`(() => {
          const row = document.querySelector('#job-list .job-row[data-status="${status}"]');
          if (!row) return false;
          const button = row.querySelector('button');
          if (!button || button.dataset.action !== '${expectedAction}') return false;
          button.click();
          return true;
        })()`);
        await new Promise((r) => setTimeout(r, 1600));
        return clicked;
      };
      const clickedStop = await clickJobAction('running', 'stop');
      const afterStop = await page.evaluate(`(() => {
        const row = document.querySelector('#job-list .job-row[data-status="interrupted"], #job-list .job-row[data-status="canceled"]');
        return {
          paused: !!row,
          action: row ? row.querySelector('button').dataset.action : '',
          toasts: Array.from(document.querySelectorAll('#toasts .toast')).map((t) => t.innerText).join(' | '),
        };
      })()`);
      check(clickedStop, 'jobs: pause button clicked');
      check(afterStop.paused && afterStop.action === 'resume', 'jobs: POST /api/jobs/{id}/stop -> paused card offers resume', JSON.stringify(afterStop));
      check(/приостановлено/i.test(afterStop.toasts), 'jobs: stop toast text', afterStop.toasts);

      const pausedStatus = await page.evaluate(`(() => {
        const row = document.querySelector('#job-list .job-row[data-status="interrupted"], #job-list .job-row[data-status="canceled"]');
        return row ? row.dataset.status : '';
      })()`);
      const clickedResume = pausedStatus ? await clickJobAction(pausedStatus, 'resume') : false;
      const afterResume = await page.evaluate(`(() => ({
        running: !!document.querySelector('#job-list .job-row[data-status="running"]'),
        toasts: Array.from(document.querySelectorAll('#toasts .toast')).map((t) => t.innerText).join(' | '),
      }))()`);
      check(clickedResume, 'jobs: resume button clicked');
      check(afterResume.running, 'jobs: POST /api/jobs/{id}/resume -> running card', JSON.stringify(afterResume));
      check(/продолжено/i.test(afterResume.toasts), 'jobs: resume toast text', afterResume.toasts);

      // stop again so the new download below does not conflict on the destination tree
      await clickJobAction('running', 'stop');
      const dismissClicked = await page.evaluate(`(() => {
        const row = document.querySelector('#job-list .job-row[data-status="done"]');
        if (!row) return false;
        row.querySelector('button').click();
        return true;
      })()`);
      await new Promise((r) => setTimeout(r, 700));
      const afterDismiss = await page.evaluate(`(() => ({
        doneRows: document.querySelectorAll('#job-list .job-row[data-status="done"]').length,
        stored: localStorage.getItem('cloud-ferry.dismissed-jobs') || '',
      }))()`);
      check(dismissClicked && afterDismiss.doneRows === 0, 'jobs: done card dismissed from list (client-side)', JSON.stringify(afterDismiss));
      await page.screenshot(`${OUT}/home-job-actions-1440.png`);
      await reportConsole(page, '1440 jobs');
    }

    if (PHASE === 'home' || PHASE === 'flow') {
      const flow = await openFlow(page);
      check(flow.open && flow.context === 'flow-source', 'flow: opens at source step', JSON.stringify({ open: flow.open, context: flow.context }));
      check(flow.rows.length === 4, 'flow source: cloud folders from /api/accounts/a1/folders', JSON.stringify(flow.rows));
      check(flow.metas.length >= 1 && flow.metas.every((m) => /файл/.test(m) && /Б|КБ|МБ|ГБ/.test(m)), 'flow source: folder sizes from /api/cloud/sizes', JSON.stringify(flow.metas));
      check(flow.tags.length >= 1, 'flow source: "already on server" tags from /api/jobs/existing', JSON.stringify(flow.tags));
      await page.screenshot(`${OUT}/flow-source-1440.png`);
      // nested folders via chevron: GET /api/accounts/{id}/folders?path=...
      const expandClicked = await page.evaluate(`(() => {
        const button = document.querySelector('.folder-expand[data-expand="Фотографии"]');
        if (!button) return false;
        button.click();
        return true;
      })()`);
      await new Promise((r) => setTimeout(r, 1200));
      const nested = await page.evaluate(`(() => ({
        rows: Array.from(document.querySelectorAll('#flow-source-tree .folder-check')).map((el) => el.dataset.path),
        children: !!document.querySelector('.folder-children'),
      }))()`);
      check(expandClicked && nested.rows.some((p) => p.includes('2024')), 'flow source: nested cloud folders loaded', JSON.stringify(nested));
      // file panel: GET /api/accounts/{id}/files
      const eyeClicked = await page.evaluate(`(() => {
        const button = document.querySelector('.folder-files-toggle[data-preview="Фотографии"]');
        if (!button) return false;
        button.click();
        return true;
      })()`);
      await new Promise((r) => setTimeout(r, 1400));
      const filesPanel = await page.evaluate(`(() => ({
        panel: !!document.querySelector('.files-panel'),
        files: Array.from(document.querySelectorAll('.files-panel .file-name')).map((el) => el.textContent),
        error: document.querySelector('.files-state--error') ? document.querySelector('.files-state--error').innerText : '',
      }))()`);
      check(eyeClicked && filesPanel.files.some((f) => f.includes('cover.jpg')), 'flow source: file preview from /api/accounts/{id}/files', JSON.stringify(filesPanel));
      await page.screenshot(`${OUT}/flow-files-1440.png`);
      await page.evaluate(`(() => { const b = document.querySelector('[data-files-close]'); if (b) b.click(); return true; })()`);
      await new Promise((r) => setTimeout(r, 400));
      await reportConsole(page, '1440 flow source');

      // select a folder and continue to review
      await page.evaluate(`(() => { const cb = document.querySelector('.folder-check[data-path="Документы"]'); cb.click(); return true; })()`);
      await new Promise((r) => setTimeout(r, 500));
      const afterSelect = await page.evaluate(`(() => { const p = document.querySelector('#folder-choose'); return { text: p.textContent.trim(), disabled: p.disabled }; })()`);
      check(afterSelect.disabled === false, 'flow source: continue enabled after selection', JSON.stringify(afterSelect));
      await page.evaluate(`document.querySelector('#folder-choose').click(); true`);
      await new Promise((r) => setTimeout(r, 2500));
      const review = await page.evaluate(`(() => {
        const dlg = document.querySelector('#folder-dialog');
        return {
          context: dlg.dataset.context,
          text: document.querySelector('#review-body').innerText,
          estimate: document.querySelector('#review-estimate') ? document.querySelector('#review-estimate').textContent : null,
          primary: (() => { const p = document.querySelector('#folder-choose'); return { text: p.textContent.trim(), disabled: p.disabled }; })(),
          error: document.querySelector('#review-error').hidden ? '' : document.querySelector('#review-error').textContent,
        };
      })()`);
      check(review.context === 'flow-review', 'flow: review opened after preflight', JSON.stringify({ context: review.context, error: review.error }));
      check(/файл/.test(review.text) && /·/.test(review.text), 'flow review: preflight shows files/bytes', review.text);
      check(!/Не удалось оценить объём/.test(review.text), 'flow review: preflight did not fail', review.text);
      await page.screenshot(`${OUT}/flow-review-1440.png`);
      await reportConsole(page, '1440 flow review');

      const jobCountBefore = await page.evaluate(`document.querySelectorAll('#job-list .job-row').length`);
      await page.evaluate(`document.querySelector('#folder-choose').click(); true`);
      await new Promise((r) => setTimeout(r, 2200));
      const afterStart = await page.evaluate(`(() => ({
        dialogOpen: document.querySelector('#folder-dialog').open,
        toasts: Array.from(document.querySelectorAll('#toasts .toast')).map((t) => t.innerText),
        jobCount: document.querySelectorAll('#job-list .job-row').length,
        globalToasts: Array.from(document.querySelectorAll('#toasts-flow .toast')).map((t) => t.innerText),
        jobCountBefore: ${jobCountBefore},
      }))()`);
      const createdApi = page.apiResponses.filter((r) => r.url.endsWith('/api/jobs'));
      check(createdApi.some((r) => r.status === 201), 'flow: POST /api/jobs returned 201', JSON.stringify(createdApi));
      check(!afterStart.dialogOpen, 'flow: dialog closed after job start', JSON.stringify(afterStart));
      const started = [...afterStart.toasts, ...afterStart.globalToasts].join(' ');
      check(/началось|запущено/i.test(started), 'flow: start toast shown', started);
      check(afterStart.jobCount === afterStart.jobCountBefore + 1, 'flow: new job card appears', JSON.stringify(afterStart));
      const newRow = await page.evaluate(`(() => {
        const rows = Array.from(document.querySelectorAll('#job-list .job-row'));
        const row = rows.find((r) => r.dataset.status === 'running');
        return row ? { status: row.dataset.status, title: row.querySelector('h2').textContent } : null;
      })()`);
      check(newRow && /Документы/.test(newRow.title), 'flow: new job card shows selected source', JSON.stringify(newRow));
      await page.screenshot(`${OUT}/home-after-start-1440.png`);
      await reportConsole(page, '1440 after start');
    }

    if (PHASE === 'all') {
      // 390 layout
      await page.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
      await page.navigate(BASE + '/', { waitMs: 2600 });
      await snapshotOverflow(page, '390 home');
      await page.screenshot(`${OUT}/home-390.png`);
      await reportConsole(page, '390 home');
      const flow390 = await openFlow(page);
      check(flow390.open, '390 flow: dialog opens', JSON.stringify({ open: flow390.open }));
      const layoutFlow = await page.evaluate(layoutExpr());
      check(layoutFlow.htmlScrollWidth === layoutFlow.innerWidth, '390 flow: no horizontal overflow',
        `scrollWidth=${layoutFlow.htmlScrollWidth} innerWidth=${layoutFlow.innerWidth}`);
      await page.screenshot(`${OUT}/flow-source-390.png`);
      await reportConsole(page, '390 flow');

      // 1440 again for the setup screen if account was removed
      await page.send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
      await page.navigate(BASE + '/#/setup', { waitMs: 1600 });
    }

    const result = {
      base: BASE,
      phase: PHASE,
      problems,
      report,
      console: {
        errors: page.consoleEntries.filter((e) => e.type === 'error'),
        exceptions: page.exceptions,
        logErrors: page.logEntries.filter((e) => e.level === 'error'),
      },
      apiResponses: page.apiResponses.map((r) => `${r.status} ${new URL(r.url).pathname}`),
    };
    writeFileSync(`${OUT}/report-${PHASE}.json`, JSON.stringify(result, null, 2));
    console.log(JSON.stringify({ ok: problems.length === 0, problems, checks: report.length }, null, 2));
    for (const p of problems) console.log('PROBLEM:', p);
    process.exitCode = problems.length ? 1 : 0;
  } finally {
    await page.close();
    await stop(browser);
  }
}

main().catch((error) => {
  console.error('FATAL', error);
  process.exit(2);
});
