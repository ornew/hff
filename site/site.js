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
  var sidebar = document.getElementById('documentation-sidebar');
  var narrowNavigation = window.matchMedia('(max-width:800px)');
  var sidebarHidden = false;
  function updateNavigation() {
    var desktop = sidebar && !narrowNavigation.matches;
    if (desktop && drawer.open) drawer.close();
    menu.setAttribute('aria-controls', desktop ? sidebar.id : drawer.id);
    menu.setAttribute('aria-expanded', String(desktop ? !sidebarHidden : drawer.open));
    menu.setAttribute('aria-label', desktop ? (sidebarHidden ? 'Show navigation' : 'Hide navigation') : 'Open navigation');
    if (desktop) menu.removeAttribute('aria-haspopup');
    else menu.setAttribute('aria-haspopup', 'dialog');
    root.classList.toggle('sidebar-hidden', Boolean(desktop && sidebarHidden));
  }
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
      updateNavigation();
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
  menu.addEventListener('click', function () {
    if (sidebar && !narrowNavigation.matches) {
      sidebarHidden = !sidebarHidden;
      updateNavigation();
    } else openDialog(drawer, menu);
  });
  narrowNavigation.addEventListener('change', updateNavigation);
  updateNavigation();
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
  function searchRanges(text, terms) {
    var folded = text.toLowerCase();
    var starts = [], ends = [];
    // Map the case-folded UTF-16 offsets back to original text offsets so that
    // rendering keeps the source spelling and never needs HTML interpolation.
    for (var offset = 0; offset < text.length;) {
      var point = text.codePointAt(offset);
      var original = String.fromCodePoint(point);
      var lowered = original.toLowerCase();
      for (var i = 0; i < lowered.length; i++) {
        starts.push(offset);
        ends.push(offset + original.length);
      }
      offset += original.length;
    }
    if (starts.length !== folded.length) return [];

    var ranges = [];
    terms.forEach(function (term) {
      if (!term) return;
      var needle = term.toLowerCase(), from = 0, at;
      while ((at = folded.indexOf(needle, from)) !== -1) {
        ranges.push({ start: starts[at], end: ends[at + needle.length - 1] });
        from = at + Math.max(needle.length, 1);
      }
    });
    ranges.sort(function (a, b) { return a.start - b.start || a.end - b.end; });
    return ranges.reduce(function (merged, range) {
      var last = merged[merged.length - 1];
      if (last && range.start <= last.end) last.end = Math.max(last.end, range.end);
      else merged.push({ start: range.start, end: range.end });
      return merged;
    }, []);
  }
  function appendHighlighted(parent, text, terms) {
    var ranges = searchRanges(text, terms), offset = 0;
    ranges.forEach(function (range) {
      if (range.start > offset) parent.appendChild(document.createTextNode(text.slice(offset, range.start)));
      var mark = document.createElement('mark');
      mark.className = 'search-highlight';
      mark.textContent = text.slice(range.start, range.end);
      parent.appendChild(mark);
      offset = range.end;
    });
    if (offset < text.length || !ranges.length) {
      parent.appendChild(document.createTextNode(text.slice(offset)));
    }
  }
  function renderResults() {
    results.replaceChildren();
    var query = input.value.trim(), terms = query.toLowerCase().split(/\s+/).filter(Boolean), hits = rank(query);
    status.textContent = query ? (hits.length ? hits.length + ' matching pages' : 'No matches. Try another word.') : 'Search the language specification and guides.';
    hits.forEach(function (hit) {
      var a = document.createElement('a');
      a.className = 'search-result';
      a.href = new URL(hit.entry.url, siteRoot).href;
      var title = document.createElement('strong');
      appendHighlighted(title, hit.entry.title, terms);
      var snippet = document.createElement('span');
      var ranges = searchRanges(hit.entry.text, terms);
      var at = ranges.length ? ranges[0].start : 0;
      var start = Math.max(0, at - 45);
      var excerpt = hit.entry.text.slice(start, start + 160);
      if (start) snippet.appendChild(document.createTextNode('…'));
      appendHighlighted(snippet, excerpt, terms);
      snippet.appendChild(document.createTextNode('…'));
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
