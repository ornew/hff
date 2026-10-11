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

  // Lexical highlighting only; source is always inserted as text, never HTML.
  var goKeywords = new Set(('break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var nil true false iota').split(' '));
  var goTypes = new Set(('any bool byte comparable complex64 complex128 error float32 float64 int int8 int16 int32 int64 rune string uint uint8 uint16 uint32 uint64 uintptr').split(' '));
  var rules = {
    hff: [
      ['string', /"(?:\\.|[^"\\])*"/y],
      ['name', /`(?:\\.|[^`\\])*`/y],
      ['keyword', /\b(?:true|false|null)\b/y],
      ['number', /\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b/y],
      ['op', /!~|!=|>=|<=|=>|[=~<>|:+*!%\/-]/y]
    ],
    go: [
      ['comment', /\/\/[^\n]*|\/\*[\s\S]*?(?:\*\/|$)/y],
      ['string', /"(?:\\.|[^"\\\n])*"|`[^`]*`|'(?:\\.|[^'\\\n])*'/y],
      ['number', /\b\d[\w.]*(?:[eEpP][+-]?\d+)?/y],
      ['ident', /[\p{L}_][\p{L}\p{N}_]*/uy]
    ],
    json: [
      ['string', /"(?:\\.|[^"\\])*"/y],
      ['number', /-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/y],
      ['keyword', /\b(?:true|false|null)\b/y]
    ],
    sh: [
      ['comment', /(?:^|(?<=\s))#[^\n]*/y],
      ['string', /"(?:\\.|[^"\\])*"|'[^']*'/y],
      ['type', /\$\{[^}]*\}|\$[A-Za-z_][A-Za-z_0-9]*/y]
    ],
    sql: [
      ['comment', /--[^\n]*|\/\*[\s\S]*?(?:\*\/|$)/y],
      ['string', /'(?:''|[^'])*'/y],
      ['name', /"(?:""|[^"])*"/y],
      ['keyword', /\b(?:SELECT|FROM|WHERE|AND|OR|NOT|NULL|TRUE|FALSE|AS|IN|IS|LIKE|JOIN|ON|ORDER|BY|LIMIT|OFFSET|ASC|DESC)\b/iy],
      ['number', /\b\d+(?:\.\d+)?\b/y]
    ],
    toml: [
      ['comment', /#[^\n]*/y],
      ['string', /"(?:\\.|[^"\\])*"|'[^']*'/y],
      ['keyword', /\b(?:true|false)\b/y],
      ['number', /[+-]?\b\d+(?:\.\d+)?\b/y]
    ]
  };
  rules.golang = rules.go;
  rules.bash = rules.shell = rules.console = rules.sh;
  document.querySelectorAll('pre code[class*="language-"]').forEach(function (code) {
    var language = Array.from(code.classList).find(function (name) { return name.startsWith('language-'); });
    var languageRules = rules[language.slice(9).toLowerCase()];
    if (!languageRules) return;
    var source = code.textContent, fragment = document.createDocumentFragment();
    var offset = 0, plainStart = 0;
    while (offset < source.length) {
      var token = null, kind;
      for (var rule of languageRules) {
        rule[1].lastIndex = offset;
        token = rule[1].exec(source);
        if (token) { kind = rule[0]; break; }
      }
      if (!token) { offset++; continue; }
      if (kind === 'ident') {
        kind = goKeywords.has(token[0]) ? 'keyword' : goTypes.has(token[0]) ? 'type' : /^\s*\(/.test(source.slice(offset + token[0].length)) ? 'function' : null;
      }
      if (kind) {
        fragment.append(document.createTextNode(source.slice(plainStart, offset)));
        var span = document.createElement('span');
        span.className = 'token-' + kind;
        span.textContent = token[0];
        fragment.append(span);
        plainStart = offset + token[0].length;
      }
      offset += token[0].length;
    }
    fragment.append(document.createTextNode(source.slice(plainStart)));
    code.replaceChildren(fragment);
  });
})();
