// Scenario 2: folder creation, "whole disk" exclusions, setup flow, account lifecycle.
import { launch, newPage, stop } from './cdp.mjs';
import { mkdirSync, writeFileSync } from 'node:fs';

const MAIN = process.argv[2] || 'http://127.0.0.1:8877';
const EMPTY = process.argv[3] || 'http://127.0.0.1:8878';
const OUT = process.argv[4] || '/tmp/cf-test/shots';
mkdirSync(OUT, { recursive: true });

const report = [];
const problems = [];
function check(cond, name, detail = '') {
  report.push({ name, ok: !!cond, detail });
  if (!cond) problems.push(`${name}: ${detail}`);
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function reportConsole(page, label) {
  const errs = page.consoleEntries.filter((e) => e.type === 'error');
  const ex = page.exceptions;
  check(errs.length === 0, `console ${label}: no console.error`, JSON.stringify(errs));
  check(ex.length === 0, `console ${label}: no uncaught exceptions`, JSON.stringify(ex));
  const bad4 = page.apiResponses.filter((r) => r.status >= 400);
  return bad4;
}

async function openFlow(page) {
  await page.evaluate(`document.querySelector('#flow-start').click(); true`);
  await sleep(1500);
  return page.evaluate(`(() => ({
    context: document.querySelector('#folder-dialog').dataset.context,
    open: document.querySelector('#folder-dialog').open,
  }))()`);
}

async function main() {
  const browser = await launch({ port: 9335, width: 1440, height: 900 });
  // --- main env: create folder + exclusions ---
  const page = await newPage(browser.base, 'about:blank', 1440, 900);
  try {
    await page.navigate(MAIN + '/', { waitMs: 2400 });
    const flow = await openFlow(page);
    check(flow.open && flow.context === 'flow-source', 'main: flow opens', JSON.stringify(flow));
    // back to destination step
    await page.evaluate(`document.querySelector('#folder-back').click(); true`);
    await sleep(1200);
    const destStep = await page.evaluate(`(() => ({
      context: document.querySelector('#folder-dialog').dataset.context,
      path: document.querySelector('#folder-path').textContent,
      rows: Array.from(document.querySelectorAll('#flow-dest-body .folder-row')).map((r) => r.dataset.folderPath),
      createVisible: !document.querySelector('#folder-create').hidden,
    }))()`);
    check(destStep.context === 'flow-dest', 'main: back reaches destination step', JSON.stringify(destStep));
    check(destStep.createVisible, 'main: create folder button visible on destination step');
    check(destStep.rows.some((p) => p.includes('Архив')), 'main: destination dirs from /api/fs/folders', JSON.stringify(destStep.rows));

    // create folder through the dialog
    await page.evaluate(`document.querySelector('#folder-create').click(); true`);
    await page.evaluate(`(() => { const i = document.querySelector('#folder-new-name'); i.value = 'Браузер'; i.dispatchEvent(new Event('input', {bubbles:true})); document.querySelector('#folder-create-submit').click(); return true; })()`);
    await sleep(1200);
    const created = await page.evaluate(`(() => ({
      path: document.querySelector('#folder-path').textContent,
      rows: Array.from(document.querySelectorAll('#flow-dest-body .folder-row')).map((r) => r.dataset.folderPath),
      toast: Array.from(document.querySelectorAll('#toasts-flow .toast, #toasts .toast')).map((t) => t.innerText).join(' '),
      error: document.querySelector('#folder-create-error').hidden ? '' : document.querySelector('#folder-create-error').textContent,
    }))()`);
    check(/Браузер/.test(created.path), 'main: POST /api/fs/dirs created and opened folder', JSON.stringify(created));
    await page.evaluate(`document.querySelector('#folder-up').click(); true`);
    await sleep(1000);
    const parent = await page.evaluate(`(() => ({
      path: document.querySelector('#folder-path').textContent,
      rows: Array.from(document.querySelectorAll('#flow-dest-body .folder-row')).map((r) => r.dataset.folderPath),
    }))()`);
    check(parent.rows.some((p) => p.endsWith('/Браузер')), 'main: new folder listed after create', JSON.stringify(parent));

    // confirm destination, go to source, select whole disk, try to exclude one folder
    await page.evaluate(`document.querySelector('#folder-choose').click(); true`);
    await sleep(1600);
    await page.evaluate(`document.querySelector('#select-disk').click(); true`);
    await sleep(900);
    const allState = await page.evaluate(`(() => ({
      checks: Array.from(document.querySelectorAll('#flow-source-tree .folder-check')).map((cb) => ({ path: cb.dataset.path, checked: cb.checked, disabled: cb.disabled })),
      footer: document.querySelector('#folder-choose').textContent.trim(),
    }))()`);
    check(allState.checks.every((c) => c.checked), 'main: whole disk checks all folders', JSON.stringify(allState.checks));
    check(allState.checks.every((c) => c.disabled), 'main: folders disabled under whole disk (no exclusions supported by backend)', JSON.stringify(allState.checks));
    const excludeAttempt = await page.evaluate(`(() => {
      const cb = document.querySelector('.folder-check[data-path="Документы"]');
      cb.checked = false;
      cb.dispatchEvent(new Event('change', { bubbles: true }));
      return { checked: cb.checked, disabled: cb.disabled };
    })()`);
    const afterAttempt = await page.evaluate(`(() => {
      const cb = document.querySelector('.folder-check[data-path="Документы"]');
      return { checked: cb.checked, disabled: cb.disabled };
    })()`);
    check(afterAttempt.checked === true && afterAttempt.disabled === true,
      'main: unchecking a folder under whole disk does not exclude it', JSON.stringify(afterAttempt));

    // continue to review, verify no excludedFolders field is sent
    page.apiResponses.length = 0;
    await page.evaluate(`document.querySelector('#folder-choose').click(); true`);
    await sleep(2200);
    const review = await page.evaluate(`(() => ({
      context: document.querySelector('#folder-dialog').dataset.context,
      text: document.querySelector('#review-body').innerText,
      error: document.querySelector('#review-error').hidden ? '' : document.querySelector('#review-error').textContent,
    }))()`);
    check(review.context === 'flow-review', 'main: review opens with whole disk', JSON.stringify({ context: review.context, error: review.error }));
    check(!/Не удалось оценить/.test(review.text), 'main: preflight succeeds for whole disk', review.text);
    const preflightCalls = page.apiResponses.filter((r) => r.url.includes('/api/preflight'));
    check(preflightCalls.every((r) => r.status === 200), 'main: POST /api/preflight accepted (no unknown fields)', JSON.stringify(preflightCalls));
    const bad = await reportConsole(page, 'main flow');
    check(bad.filter((r) => r.url.includes('/api/preflight')).length === 0, 'main: no 400 from preflight', JSON.stringify(bad));
    await page.screenshot(`${OUT}/flow-review-whole-disk-1440.png`);

    // close flow
    await page.evaluate(`document.querySelector('#folder-dialog [data-dialog-close]').click(); true`);
    await sleep(500);

    // --- empty env: setup flow ---
    const setupPage = await newPage(browser.base, 'about:blank', 1440, 900);
    await setupPage.navigate(EMPTY + '/', { waitMs: 2400 });
    const empty = await setupPage.evaluate(`(() => ({
      appHidden: document.querySelector('#app-view').hidden,
      text: document.querySelector('#app-view').innerText,
      plus: !!document.querySelector('.plus-tile'),
    }))()`);
    check(!empty.appHidden && empty.plus, 'setup: empty state shows connect tile', JSON.stringify(empty));
    check(!/\d+\s*%|\d+,\d+\s*ГБ/.test(empty.text), 'setup: empty state has no invented numbers', JSON.stringify(empty.text.slice(0, 300)));
    await setupPage.evaluate(`document.querySelector('.plus-tile').click(); true`);
    await sleep(800);
    const wizard = await setupPage.evaluate(`(() => ({
      hash: location.hash,
      setupHidden: document.querySelector('#setup-view').hidden,
      title: document.querySelector('#setup-title') ? document.querySelector('#setup-title').textContent : '',
      methods: Array.from(document.querySelectorAll('.method-option')).map((b) => b.dataset.method),
      next: document.querySelector('#setup-next').textContent.trim(),
      stepText: document.querySelector('#setup-step-text').textContent,
    }))()`);
    check(!wizard.setupHidden && wizard.title === 'Подключение', 'setup: connection step shown', JSON.stringify(wizard));
    check(wizard.methods.includes('webdav') && !wizard.methods.includes('yandex'), 'setup: yandex method hidden without server OAuth app', JSON.stringify(wizard.methods));
    check(wizard.stepText === 'Подключение', 'setup: step label from design (step 0)', wizard.stepText);

    await setupPage.evaluate(`(() => {
      const u = document.querySelector('#f-user'); u.value = 'browser-test';
      u.dispatchEvent(new Event('input', { bubbles: true }));
      const p = document.querySelector('#f-pass'); p.value = 'app-password';
      p.dispatchEvent(new Event('input', { bubbles: true }));
      return true;
    })()`);
    await setupPage.evaluate(`document.querySelector('#setup-next').click(); true`);
    await sleep(2500);
    const connected = await setupPage.evaluate(`(() => ({
      hash: location.hash,
      appHidden: document.querySelector('#app-view').hidden,
      setupHidden: document.querySelector('#setup-view').hidden,
      account: document.querySelector('.drive-row .drive-name h2') ? document.querySelector('.drive-row .drive-name h2').textContent : null,
      error: document.querySelector('#setup-error').textContent,
    }))()`);
    check(!connected.appHidden && connected.setupHidden, 'setup: connect returns to main screen', JSON.stringify(connected));
    check(connected.account === 'browser-test', 'setup: account login from POST /api/accounts', JSON.stringify(connected));
    const setupApi = setupPage.apiResponses.map((r) => `${r.status} ${new URL(r.url).pathname}`);
    check(setupApi.some((line) => /200 \/api\/accounts\/validate/.test(line)), 'setup: POST /api/accounts/validate called', JSON.stringify(setupApi));
    check(setupApi.some((line) => /201 \/api\/accounts$/.test(line)), 'setup: POST /api/accounts created account', JSON.stringify(setupApi));
    const setupBad = await reportConsole(setupPage, 'setup');
    check(setupBad.length === 0, 'setup: no HTTP errors during connect', JSON.stringify(setupBad));
    await setupPage.screenshot(`${OUT}/home-connected-1440.png`);

    // unreachable steps check: are destinations step titles reachable?
    const reachable = await setupPage.evaluate(`(() => {
      const titles = [];
      // simulate clicking through: only current step title exists
      titles.push(document.querySelector('#setup-step-text').textContent);
      return titles;
    })()`);
    check(reachable[0] === 'Подключение', 'setup: after connect wizard step stays 0 (steps 1-2 unreachable)', JSON.stringify(reachable));

    await setupPage.close();
    const result = { problems, report, api: page.apiResponses.map((r) => `${r.status} ${new URL(r.url).pathname}`) };
    writeFileSync(`${OUT}/report-scenario2.json`, JSON.stringify(result, null, 2));
    console.log(JSON.stringify({ ok: problems.length === 0, problems, checks: report.length }, null, 2));
    for (const p of problems) console.log('PROBLEM:', p);
    process.exitCode = problems.length ? 1 : 0;
  } finally {
    await page.close();
    await stop(browser);
  }
}

main().catch((error) => { console.error('FATAL', error); process.exit(2); });
