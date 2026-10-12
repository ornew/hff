// Apply the saved preference before paint. Storage may be unavailable on file URLs.
(function () {
  var mode = 'system';
  try { mode = localStorage.getItem('hff-theme') || 'system'; } catch (_) {}
  if (!['light', 'dark', 'system'].includes(mode)) mode = 'system';
  document.documentElement.dataset.theme = mode;
})();
