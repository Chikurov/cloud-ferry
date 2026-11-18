// Minimal Chrome DevTools Protocol driver for browser checks.
// Node >= 22 provides global fetch and WebSocket.
import { spawn } from 'node:child_process';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const CHROMIUM = '/Applications/Chromium.app/Contents/MacOS/Chromium';

export async function launch({ port = 9333, width = 1440, height = 900, chromium = CHROMIUM } = {}) {
  const profile = mkdtempSync(join(tmpdir(), 'cf-chrome-'));
  const proc = spawn(chromium, [
    '--headless=new',
    `--remote-debugging-port=${port}`,
    `--user-data-dir=${profile}`,
    '--no-first-run',
    '--no-default-browser-check',
    '--disable-gpu',
    '--no-sandbox',
    '--disable-dev-shm-usage',
    '--hide-scrollbars',
    '--force-device-scale-factor=1',
    `--window-size=${width},${height}`,
    'about:blank',
  ], { stdio: 'ignore' });
  const base = `http://127.0.0.1:${port}`;
  for (let i = 0; i < 100; i += 1) {
    try {
      const res = await fetch(`${base}/json/version`);
      if (res.ok) {
        const version = await res.json();
        return { proc, base, version, profile };
      }
    } catch { /* not up yet */ }
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error('Chromium did not start');
}

export async function newPage(base, url = 'about:blank', width = 1440, height = 900) {
  const res = await fetch(`${base}/json/new?${encodeURIComponent(url)}`, { method: 'PUT' });
  const target = await res.json();
  const ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve, { once: true });
    ws.addEventListener('error', reject, { once: true });
  });
  let nextId = 0;
  const pending = new Map();
  const events = [];
  const send = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++nextId;
    pending.set(id, { resolve, reject, method });
    ws.send(JSON.stringify({ id, method, params }));
  });

  const consoleEntries = [];
  const exceptions = [];
  const logEntries = [];
  const httpFailures = [];
  const apiResponses = [];
  const onEvent = (msg) => {
    if (msg.method === 'Runtime.consoleAPICalled') {
      const type = msg.params.type;
      const args = (msg.params.args || []).map((arg) => (
        arg.value !== undefined ? String(arg.value) : (arg.description || arg.type)
      ));
      consoleEntries.push({ type, text: args.join(' ') });
    } else if (msg.method === 'Runtime.exceptionThrown') {
      const d = msg.params.exceptionDetails;
      exceptions.push({
        text: d.text,
        description: d.exception && d.exception.description,
        url: d.url,
        line: d.lineNumber,
      });
    } else if (msg.method === 'Log.entryAdded') {
      const e = msg.params.entry;
      logEntries.push({ level: e.level, text: e.text, url: e.url, source: e.source });
    } else if (msg.method === 'Network.responseReceived') {
      const r = msg.params.response;
      if (r.url.includes('/api/')) {
        apiResponses.push({ url: r.url, status: r.status, type: msg.params.type, requestId: msg.params.requestId });
      }
      if (r.status >= 400) httpFailures.push({ url: r.url, status: r.status, requestId: msg.params.requestId });
    } else if (msg.method === 'Network.loadingFailed') {
      const p = msg.params;
      if (!p.canceled) httpFailures.push({ url: p.requestId, error: p.errorText });
    }
  };
  ws.addEventListener('message', (event) => {
    const msg = JSON.parse(event.data);
    onEvent(msg);
    const entry = pending.get(msg.id);
    if (entry) {
      pending.delete(msg.id);
      if (msg.error) entry.reject(new Error(`${entry.method}: ${msg.error.message}`));
      else entry.resolve(msg.result);
      return;
    }
    events.push(msg);
  });

  await send('Page.enable');
  await send('Runtime.enable');
  await send('Log.enable');
  await send('Network.enable');
  await send('Emulation.setDeviceMetricsOverride', {
    width, height, deviceScaleFactor: 1, mobile: width < 600,
  });

  const evaluate = async (expression, { awaitPromise = true } = {}) => {
    const result = await send('Runtime.evaluate', {
      expression, returnByValue: true, awaitPromise, userGesture: true,
    });
    if (result.exceptionDetails) {
      throw new Error(result.exceptionDetails.exception ? result.exceptionDetails.exception.description : result.exceptionDetails.text);
    }
    return result.result ? result.result.value : undefined;
  };

  const navigate = async (url, { waitMs = 1500 } = {}) => {
    await send('Page.navigate', { url });
    await new Promise((resolve) => setTimeout(resolve, waitMs));
  };

  const screenshot = async (path) => {
    const shot = await send('Page.captureScreenshot', { format: 'png' });
    const { writeFileSync } = await import('node:fs');
    writeFileSync(path, Buffer.from(shot.data, 'base64'));
    return path;
  };

  const close = async () => {
    try { ws.close(); } catch { /* closed */ }
    try { await fetch(`${base}/json/close/${target.id}`); } catch { /* ignore */ }
  };

  return {
    target, send, evaluate, navigate, screenshot, close,
    consoleEntries, exceptions, logEntries, httpFailures, apiResponses, events,
  };
}

export async function stop(browser) {
  try { browser.proc.kill('SIGKILL'); } catch { /* gone */ }
}
