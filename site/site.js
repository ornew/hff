(function () {
  'use strict';
  var root = document.documentElement;
  var themeButton = document.querySelector('.theme-toggle');
  var modes = ['system', 'light', 'dark'];
  function updateThemeLabel() {
    var mode = root.dataset.theme || 'system';
    themeButton.setAttribute('aria-label', 'Color mode: ' + mode + '. Click to change.');
    themeButton.title = 'Color mode: ' + mode;
    themeButton.dataset.mode = mode;
  }
  themeButton.addEventListener('click', function () {
    var next = modes[(modes.indexOf(root.dataset.theme || 'system') + 1) % modes.length];
    root.dataset.theme = next;
    try { localStorage.setItem('hff-theme', next); } catch (_) {}
    updateThemeLabel();
  });
  updateThemeLabel();

  var drawer = document.getElementById('navigation-dialog');
  var menu = document.querySelector('.menu-toggle');
  var search = document.getElementById('search-dialog');
  var input = document.getElementById('search-input');
  var results = document.getElementById('search-results');
  var status = document.getElementById('search-status');
  var opener = null;
  function openDialog(dialog, button) {
    opener = button || document.activeElement;
    dialog.showModal();
    document.body.classList.add('modal-open');
    if (dialog === drawer) menu.setAttribute('aria-expanded', 'true');
    if (dialog === search) input.focus();
  }
  function closeDialog(dialog) { dialog.close(); }
  [drawer, search].forEach(function (dialog) {
    dialog.addEventListener('close', function () {
      document.body.classList.remove('modal-open');
      menu.setAttribute('aria-expanded', 'false');
      if (opener) opener.focus();
    });
    dialog.addEventListener('click', function (event) {
      if (event.target !== dialog) return;
      var rect = dialog.getBoundingClientRect();
      if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) closeDialog(dialog);
    });
  });
  document.querySelectorAll('[data-close]').forEach(function (button) {
    button.addEventListener('click', function () { closeDialog(document.getElementById(button.dataset.close)); });
  });
  menu.setAttribute('aria-expanded', 'false');
  menu.addEventListener('click', function () { openDialog(drawer, menu); });
  drawer.querySelectorAll('a').forEach(function (link) { link.addEventListener('click', function () { closeDialog(drawer); }); });
  document.querySelector('.search-trigger').addEventListener('click', function (event) { openDialog(search, event.currentTarget); });
  document.addEventListener('keydown', function (event) {
    var editing = /INPUT|TEXTAREA|SELECT/.test(event.target.tagName) || event.target.isContentEditable;
    if (!editing && (event.key === '/' || ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k'))) {
      event.preventDefault();
      if (!search.open && !drawer.open) openDialog(search);
    }
  });
  var siteRoot = new URL(root.dataset.root, window.location.href);
  function rank(query) {
    var terms = query.toLowerCase().trim().split(/\s+/).filter(Boolean);
    if (!terms.length) return [];
    var hits = [];
    (window.HFF_SEARCH_INDEX || []).forEach(function (entry) {
      var title = entry.title.toLowerCase(), body = entry.text.toLowerCase(), score = 0;
      for (var term of terms) {
        if (!title.includes(term) && !body.includes(term)) return;
        score += title.includes(term) ? 30 : 1;
      }
      hits.push({ entry: entry, score: score });
    });
    hits.sort(function (a, b) { return b.score - a.score || a.entry.url.localeCompare(b.entry.url); });
    var seen = new Set();
    return hits.filter(function (hit) {
      var page = hit.entry.url.split('#')[0];
      if (seen.has(page)) return false;
      seen.add(page); return true;
    }).slice(0, 8);
  }
  function renderResults() {
    results.replaceChildren();
    var query = input.value.trim(), hits = rank(query);
    status.textContent = query ? (hits.length ? hits.length + ' matching pages' : 'No matches. Try another word.') : 'Search the language specification and guides.';
    hits.forEach(function (hit) {
      var a = document.createElement('a');
      a.className = 'search-result';
      a.href = new URL(hit.entry.url, siteRoot).href;
      var title = document.createElement('strong'); title.textContent = hit.entry.title;
      var snippet = document.createElement('span');
      var at = hit.entry.text.toLowerCase().indexOf(query.toLowerCase().split(/\s+/)[0]);
      var start = Math.max(0, at - 45);
      snippet.textContent = (start ? '…' : '') + hit.entry.text.slice(start, start + 160) + '…';
      a.append(title, snippet);
      a.addEventListener('click', function () { closeDialog(search); });
      results.append(a);
    });
  }
  input.addEventListener('input', renderResults);
  search.addEventListener('keydown', function (event) {
    var links = Array.from(results.querySelectorAll('a'));
    var index = links.indexOf(document.activeElement);
    if (event.key === 'ArrowDown' && links.length) {
      event.preventDefault(); links[(index + 1) % links.length].focus();
    } else if (event.key === 'ArrowUp' && links.length) {
      event.preventDefault(); if (index <= 0) input.focus(); else links[index - 1].focus();
    } else if (event.key === 'Enter' && document.activeElement === input && links.length) {
      event.preventDefault(); links[0].click();
    }
  });

  // A small lexical highlighter, not a parser or an evaluator.
  document.querySelectorAll('code.language-hff').forEach(function (code) {
    var source = code.textContent;
    var tokens = /("(?:\\.|[^"\\])*"|`(?:\\.|[^`\\])*`|\b(?:true|false|null)\b|\b\d+(?:\.\d+)?\b|(?:!~|!=|>=|<=|=>|[=~>|:+*!%-]))/g;
    var fragment = document.createDocumentFragment(), previous = 0, match;
    while ((match = tokens.exec(source))) {
      fragment.append(document.createTextNode(source.slice(previous, match.index)));
      var span = document.createElement('span'), token = match[0];
      span.className = token[0] === '`' ? 'token-name' : token[0] === '"' ? 'token-string' : /^\d/.test(token) ? 'token-number' : /^(true|false|null)$/.test(token) ? 'token-keyword' : 'token-op';
      span.textContent = token; fragment.append(span); previous = tokens.lastIndex;
    }
    fragment.append(document.createTextNode(source.slice(previous)));
    code.replaceChildren(fragment);
  });
})();
