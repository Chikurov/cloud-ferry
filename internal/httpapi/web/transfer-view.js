'use strict';

function jobProgressText(job) {
  const stats = job.stats || {};
  let size = '';
  if (job.status === 'done') {
    const total = Number(stats.totalBytes) || Number(stats.bytes) || 0;
    size = total > 0 ? humanBytes(total) : '';
  } else if (Number(stats.totalBytes) > 0) {
    size = humanPair(stats.bytes, stats.totalBytes);
  } else if (Number(stats.bytes) > 0) {
    size = humanBytes(stats.bytes);
  }
  return size;
}

function renderJobList(jobs) {
  const list = $('#job-list');
  const stale = new Map(Array.from(list.children, row => [row.dataset.id, row]));
  for (const job of jobs) {
    let row = stale.get(job.id);
    stale.delete(job.id);
    if (!row) {
      row = document.createElement('article');
      row.className = 'cell cell--now job-row';
      row.dataset.id = job.id;
      row.innerHTML = `<div class="task-head">${icon('folder')}<h2></h2><button class="btn btn--secondary" type="button"></button></div>
        <div class="now-body"><div class="progress job-progress" role="progressbar" aria-valuemin="0" aria-valuemax="100"><i></i></div><div class="task-stats"><span></span><span></span></div><div class="job-error"></div><button type="button" class="btn btn--link destination-link" data-action="change-dest" hidden>Папка сохранения</button></div>`;
      list.append(row);
    }
    const paths = [...jobSourcePaths(job), ...jobSourceFiles(job)];
    const title = job.excludedFolders?.length ? wholeDiskLabel(job.excludedFolders) : paths.length === 1 ? shortPath(paths[0]) : jobSourceLabel(job);
    row.querySelector('h2').textContent = title;
    const button = row.querySelector('button');
    const paused = PAUSED_STATUSES.includes(job.status);
    const failed = job.status === 'failed';
    const done = job.status === 'done';
    const label = done ? 'Убрать из списка' : paused ? (failed ? 'Повторить' : 'Продолжить') : 'Приостановить';
    if (row.dataset.status !== job.status) {
      row.dataset.status = job.status;
      button.hidden = job.status === 'verifying';
      button.dataset.id = job.id;
      button.dataset.action = done ? 'dismiss-job' : paused ? 'resume' : 'stop';
      button.title = label;
      button.setAttribute('aria-label', label + ' · ' + title);
      button.innerHTML = icon(done ? 'close' : paused ? 'resume' : 'pause');
      /* В очереди кнопка остаётся доступной — пауза сработает, — но
         aria-busy честно говорит, что запуск ещё не завершён. */
      button.setAttribute('aria-busy', job.status === 'queued' ? 'true' : 'false');
      const change = row.querySelector('.destination-link');
      change.hidden = !paused || job.mode === 'sync';
      change.dataset.id = job.id;
      const error = row.querySelector('.job-error');
      error.hidden = !failed;
      if (failed) error.innerHTML = escapeHtml(job.errorAdvice || job.errorTitle || 'Не удалось скачать') + failureListMarkup(job.failures || [], job.failureCount || 0) +
        (job.failureCount ? `<button class="btn btn--quiet" data-action="copy-failures" data-id="${escapeHtml(job.id)}">Скопировать список</button>` : '');
    }
    /* Пока движок готовится или пересканирует облако, полоса показывает, что
       работа идёт, а старый процент остаётся видимым и приглушённым. */
    const preparing = jobPreparing(job);
    const percent = shownPercent(job);
    const known = done || Number(job.stats?.totalBytes) > 0 || percent > 0;
    const stateText = jobStateText(job);
    const progress = row.querySelector('.job-progress');
    progress.setAttribute('aria-label', title);
    progress.classList.toggle('progress--prepare', preparing);
    progress.querySelector('i').style.width = `${known ? percent : 0}%`;
    if (known) progress.setAttribute('aria-valuenow', String(percent));
    else progress.removeAttribute('aria-valuenow');
    const valueText = [known ? `${percent} %` : '', stateText].filter(Boolean).join(', ');
    if (valueText) progress.setAttribute('aria-valuetext', valueText);
    else progress.removeAttribute('aria-valuetext');
    const facts = row.querySelectorAll('.task-stats span');
    facts[0].textContent = jobProgressText(job);
    facts[1].textContent = stateText;

  }
  jobs.forEach((job, index) => {
    const row = list.children[index];
    if (row && row.dataset.id !== job.id) {
      const target = list.querySelector(`[data-id="${cssEscape(job.id)}"]`);
      if (target) list.insertBefore(target, row);
    }
  });
  for (const row of stale.values()) {
    const hadFocus = row.contains(document.activeElement);
    row.remove();
    if (hadFocus) {
      const target = list.querySelector('button:not([hidden])') || $('#flow-start');
      target?.focus({ preventScroll: true });
    }
  }
  list.hidden = !jobs.length;
}
