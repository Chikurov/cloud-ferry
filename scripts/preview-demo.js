/* Offline demonstration transport. This script is embedded only in preview.html. */
(() => {
  const gb = 1024 ** 3;
  const mb = 1024 ** 2;
  const params = new URLSearchParams(location.search);
  const shot = params.get('shot') || '';
  const freeze = params.has('freeze') || !!shot;
  const catalogue = [
    ['Фотографии/Рига/Утро.jpg', 3 * gb], ['Фотографии/Лето.jpg', 5 * gb],
    ['Видео/Концерт.mp4', 6 * gb], ['Документы/Архив.pdf', 140 * mb],
    ['Музыка/Альбом.flac', gb], ['Заметки.txt', 8240],
  ];
  const accounts = [{ id: 'personal', login: 'alex.demo', provider: 'yandex-oauth', status: 'ok', name: 'Яндекс Диск' }];
  if (!shot || shot === 'connect') {
    accounts.push({ id: 'work', login: 'studio.demo', provider: 'yandex-token', status: 'ok', name: 'Яндекс Диск' });
  }
  const dirs = new Set(['/downloads', '/downloads/Новые', '/downloads/Видео', '/downloads/Музыка', '/downloads/Фото', '/downloads/Документы']);
  const jobs = [];
  let uid = 0;
  let last = Date.now();
  let device = null;
  function selected(body) {
    const dirs = body.sourcePaths || [], files = body.sourceFiles || [];
    return catalogue.filter(([name]) =>
      !(body.excludedFolders || []).some(root => name.startsWith(root + '/')) &&
      ((!dirs.length && !files.length) || files.includes(name) || dirs.some(root => name.startsWith(root + '/'))));
  }
  function job(accountId, source, dest, status, bytes, speed = 12) {
    const request = { accountId, ...source };
    const total = selected(request).reduce((sum, [, size]) => sum + size, 0);
    const eta = status === 'running' && speed > 0
      ? Math.ceil((total - bytes) / (speed * mb))
      : 0;
    return { ...request, id: 'preview-' + ++uid, mode: 'copy', status, dest, createdAt: new Date().toISOString(),
      stats: { bytes, totalBytes: total, elapsedTime: bytes ? 180 : 0, speed: status === 'running' ? speed * mb : 0, eta } };
  }
  const homeRunning = job('personal', { sourcePaths: ['Фотографии'] }, '/downloads/Фото', 'running', 3.2 * gb, 8.5);
  if (shot === 'home') {
    jobs.push(homeRunning);
  } else if (shot === 'downloads' || shot === 'select' || shot === 'destination' || !shot) {
    jobs.push(
      homeRunning,
      job('personal', { sourcePaths: ['Видео'] }, '/downloads/Видео', 'queued', 0, 0),
      job('personal', { sourcePaths: ['Документы'] }, '/downloads/Документы', 'canceled', 70 * mb, 0),
      job('personal', { sourcePaths: ['Музыка'] }, '/downloads/Музыка', 'done', gb, 0),
    );
    if (jobs[1]) jobs[1].startsAt = freeze ? Date.now() + 86400000 : Date.now() + 45000;
  }
  if (params.has('empty') || shot === 'connect') { accounts.length = 0; jobs.length = 0; }
  function advance() {
    if (freeze) { last = Date.now(); return; }
    const now = Date.now(), seconds = Math.max(0, (now - last) / 1000);
    for (const task of jobs) {
      if (task.status === 'queued' && now >= task.startsAt) { task.status = 'running'; continue; }
      if (task.status !== 'running') continue;
      const stats = task.stats;
      stats.elapsedTime += seconds;
      stats.bytes = Math.min(stats.totalBytes, stats.bytes + seconds * stats.speed);
      stats.eta = Math.ceil((stats.totalBytes - stats.bytes) / stats.speed);
      if (stats.bytes >= stats.totalBytes) { task.status = 'done'; task.finishedAt = new Date().toISOString(); }
    }
    last = now;
  }
  window.__CF_DEMO__ = { freeze, shot, accounts, jobs };
  const parent = path => path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '';
  const leaf = path => path.slice(path.lastIndexOf('/') + 1);
  const json = (data, status = 200) => new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
  const conflict = (dest, id) => jobs.some(j => j.id !== id && ['queued', 'running'].includes(j.status) &&
    (j.dest === dest || j.dest.startsWith(dest + '/') || dest.startsWith(j.dest + '/')));
  function addAccount(login, provider) {
    const account = { id: 'drive-' + ++uid, login: login || 'demo.' + uid, status: 'ok', provider, name: 'Яндекс Диск' };
    accounts.push(account);
    return account;
  }
  window.fetch = async (url, opts = {}) => {
    advance();
    const u = new URL(url, 'https://preview.local'), p = u.pathname, path = u.searchParams.get('path') || '';
    const body = opts.body ? JSON.parse(opts.body) : {};
    if (p === '/api/state') return json({ accounts, jobs, downloads: '/downloads/Новые' });
    if (p === '/api/system') return json({ roots: [{ path: '/downloads', free: 240 * gb, total: 500 * gb }], cloud: { accountId: accounts.at(-1)?.id, status: 'ok', used: 28 * gb, total: 100 * gb } });
    if (p === '/api/oauth/yandex/status') return json({ available: !new URLSearchParams(location.search).has('manual') });
    if (p === '/api/oauth/yandex/device/start') {
      device = { id: 'flow-' + ++uid, approved: false };
      return json({ flowId: device.id, userCode: 'DEMO3142', verificationUrl: 'https://oauth.yandex.ru/device', interval: 5, expiresIn: 300 });
    }
    if (p.startsWith('/api/oauth/yandex/device/')) {
      if (!device || p.split('/').pop() !== device.id) return json({ error: 'Код истёк' }, 404);
      if (opts.method === 'DELETE') { device = null; return json({ status: 'canceled' }); }
      if (!device.approved) return json({ status: 'pending' });
      device = null;
      return json({ status: 'done', account: addAccount('', 'yandex-oauth') });
    }
    if (p === '/api/accounts/validate') return json({ ok: true });
    if (p === '/api/accounts') return json(addAccount(body.user, body.provider), 201);
    if (p.startsWith('/api/accounts/')) {
      const [, , , id, action] = p.split('/');
      const account = accounts.find(a => a.id === id);
      if (!account) return json({ error: 'Диск отключён' }, 404);
      if (opts.method === 'DELETE') {
        accounts.splice(accounts.indexOf(account), 1);
        for (let i = jobs.length - 1; i >= 0; i--) if (jobs[i].accountId === id) jobs.splice(i, 1);
        return json({ deleted: id });
      }
      if (action === 'probe') return json({ login: account.login, about: { used: 28 * gb, total: 100 * gb } });
      if (action === 'files') return json({ files: catalogue.filter(([name]) => parent(name) === path).map(([name, size]) => ({ name: leaf(name), size })) });
      if (action === 'folders') {
        const prefix = path ? path + '/' : '';
        const children = catalogue.filter(([name]) => name.startsWith(prefix) && name.slice(prefix.length).includes('/')).map(([name]) => name.slice(prefix.length).split('/')[0]);
        return json({ dirs: [...new Set(children)].map(name => ({ name })) });
      }
    }
    if (p === '/api/jobs/existing') return json({ items: body.sourcePaths.map(path => ({ path, exists: path === 'Документы', entries: path === 'Документы' ? 1 : 0 })) });
    if (p === '/api/fs/folders') return json({ path, parent: path === '/downloads' ? null : parent(path), roots: ['/downloads'], free: 240 * gb, total: 500 * gb, writable: true, entries: [...dirs].filter(item => parent(item) === path).map(item => ({ name: leaf(item), path: item })) });
    if (p === '/api/fs/dirs') { if (dirs.has(body.path)) return json({ error: 'Такая папка уже есть' }, 409); dirs.add(body.path); return json({ path: body.path }, 201); }
    if (p === '/api/preflight') {
      const entries = selected(body);
      return json({ fits: true, files: entries.length, bytes: entries.reduce((sum, [, size]) => sum + size, 0), free: 240 * gb, freeKnown: true });
    }
    if (p === '/api/jobs') {
      const replay = jobs.find(j => j.idempotencyKey === body.clientToken && j.accountId === body.accountId);
      if (replay) return json(replay);
      if (conflict(body.destPath)) return json({ error: 'Выберите другую папку назначения.' }, 409);
      const task = job(body.accountId, body, body.destPath, 'running', 0);
      task.idempotencyKey = body.clientToken;
      jobs.unshift(task);
      return json(task, 201);
    }
    if (p.startsWith('/api/jobs/')) {
      const [, , , id, action] = p.split('/');
      const task = jobs.find(j => j.id === id);
      if (!task) return json({ error: 'Задание не найдено' }, 404);
      if (action === 'stop') { task.status = 'canceled'; return json({ stopped: id }); }
      if (action === 'resume') {
        const dest = body.destPath || task.dest;
        if (conflict(dest, id)) return json({ error: 'Папка занята другой загрузкой.' }, 409);
        if (dest !== task.dest) { task.stats.bytes = 0; task.dest = dest; }
        task.status = 'running'; return json(task);
      }
    }
    return json({ error: 'Недоступно в демо' }, 404);
  };
  document.addEventListener('click', event => {
    if (!event.target.closest('#yandex-panel .yandex-hint a')) return;
    event.preventDefault();
    const dialog = document.getElementById('demo-provider');
    dialog.showModal();
    dialog.querySelector('button').focus();
  });
  document.getElementById('demo-approve').addEventListener('click', () => {
    if (device) device.approved = true;
    document.getElementById('demo-provider').close();
  });
})();
