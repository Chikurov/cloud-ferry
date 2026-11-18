/* Тема до первого кадра. Отдельный файл, а не inline-скрипт: сервер отдаёт
   CSP script-src 'self', поэтому inline-скрипты в <head> браузер блокирует.
   Подключён в <head> до style.css, поэтому первый вычисленный кадр уже в
   выбранной теме; выбор пользователя важнее системной настройки. */
(function () {
  var saved = null;
  try { saved = localStorage.getItem('cloudferry-theme'); } catch (e) { /* хранилище недоступно */ }
  var theme = saved === 'light' || saved === 'dark'
    ? saved
    : ((window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches) ? 'dark' : 'light');
  document.documentElement.setAttribute('data-theme', theme);
  document.documentElement.style.colorScheme = theme;
  var meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.setAttribute('content', theme === 'dark' ? '#16151a' : '#f5f4f8');
})();
