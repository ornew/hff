const examples = {
  people: {
    query: 'age>=18 name',
    schema: [{ name: "name", type: "string" }, { name: "age", type: "int" }],
    records: [
      '{"name":"Alice","age":24}', '{"name":"Bob","age":17}',
      '{"name":"Chen","age":31}', '{"name":"Dana","age":18}',
      '{"name":"Erin","age":16}', '{"name":"","age":42}',
    ],
  },
  labels: {
    query: 'labels.app="auth" labels.env:in("prod","staging")',
    schema: [{ name: "service", type: "string" }, { name: "labels", type: "map[string]" }],
    records: [
      '{"service":"auth-api","labels":{"app":"auth","env":"prod","team":"identity"}}',
      '{"service":"auth-preview","labels":{"app":"auth","env":"staging"}}',
      '{"service":"auth-dev","labels":{"app":"auth","env":"dev"}}',
      '{"service":"gateway","labels":{"app":"gateway","env":"prod"}}',
      '{"service":"auth-worker","labels":{"app":"auth","env":"prod"}}',
    ],
  },
  orders: {
    query: 'items:any(item=>item.price>100 item.active)',
    schema: [{ name: "order", type: "string" }, { name: "items", type: "list[map[any]]" }],
    records: [
      '{"order":"ORD-101","items":[{"price":45,"active":true},{"price":120,"active":true}]}',
      '{"order":"ORD-102","items":[{"price":89,"active":true}]}',
      '{"order":"ORD-103","items":[{"price":250,"active":false}]}',
      '{"order":"ORD-104","items":[{"price":110,"active":true}]}',
    ],
  },
};
const query = document.querySelector("#playground-query");
const highlights = document.querySelector("#playground-query-highlights");
const schema = document.querySelector("#playground-schema");
const record = document.querySelector("#playground-record");
const select = document.querySelector("#playground-example");
const stop = document.querySelector("#playground-stop");
const status = document.querySelector("#playground-status");
const result = document.querySelector("#playground-result");
const times = ["compile", "build", "evaluate"].map(name => document.querySelector(`#playground-${name}-time`));
let worker, timer, debounceTimer, sequence = 0, active = 0, ready = false, runAfterReady = false;
function resetOutput(message = "Selected records appear here.") {
  clearHighlights();
  result.textContent = message; result.dataset.state = "empty";
  times.forEach(element => { element.textContent = "—"; });
}
function syncHighlights() {
  // Match the textarea's usable box, including when it is resized or scrolled.
  highlights.style.width = `${query.clientWidth + 2}px`;
  highlights.style.height = `${query.clientHeight + 2}px`;
  highlights.scrollTop = query.scrollTop;
  highlights.scrollLeft = query.scrollLeft;
}
function clearHighlights() {
  highlights.replaceChildren();
  query.removeAttribute("aria-invalid");
  query.removeAttribute("aria-errormessage");
}
function showHighlights(error) {
  if (error.stage !== "compile") return;
  const diagnostics = error.diagnostics?.length ? error.diagnostics : [error];
  const spans = diagnostics.filter(item => item.span).map(item => item.span);
  if (!spans.length) return;
  // Go spans use UTF-8 byte offsets; browser text uses UTF-16 code units.
  // Map all requested edges in one pass so many errors remain inexpensive.
  const edges = [...new Set(spans.flatMap(span => [span.Start, span.End]))].sort((a, b) => a - b);
  const positions = new Map();
  let edge = 0, bytes = 0, units = 0;
  for (const character of query.value) {
    const point = character.codePointAt(0);
    const width = point <= 0x7f ? 1 : point <= 0x7ff ? 2 : point <= 0xffff ? 3 : 4;
    while (edge < edges.length && edges[edge] < bytes + width) positions.set(edges[edge++], units);
    bytes += width; units += character.length;
  }
  while (edge < edges.length) positions.set(edges[edge++], units);
  const ranges = spans.map(span => ({ start: positions.get(span.Start), end: positions.get(span.End) }))
    .sort((a, b) => a.start - b.start || a.end - b.end);
  const merged = [];
  for (const range of ranges) {
    const previous = merged[merged.length - 1];
    if (previous && range.start <= previous.end) previous.end = Math.max(previous.end, range.end);
    else merged.push({ ...range });
  }
  const fragment = document.createDocumentFragment();
  let offset = 0;
  for (const range of merged) {
    fragment.append(document.createTextNode(query.value.slice(offset, range.start)));
    const mark = document.createElement("mark");
    mark.className = range.start === range.end ? "diagnostic-caret" : "diagnostic-range";
    mark.dataset.start = range.start; mark.dataset.end = range.end;
    mark.textContent = query.value.slice(range.start, range.end);
    fragment.append(mark);
    offset = range.end;
  }
  fragment.append(document.createTextNode(query.value.slice(offset) + "\n"));
  highlights.replaceChildren(fragment);
  query.setAttribute("aria-invalid", "true");
  query.setAttribute("aria-errormessage", "playground-result");
  syncHighlights();
}
query.addEventListener("scroll", syncHighlights);
new ResizeObserver(syncHighlights).observe(query);
function displayExample() {
  const example = examples[select.value];
  query.value = example.query;
  schema.value = JSON.stringify(example.schema, null, 2);
  record.value = example.records.join("\n");
  resetOutput();
}
function end() { clearTimeout(timer); active = 0; stop.hidden = true; }
function boot(thenRun = false) {
  runAfterReady = thenRun;
  ready = false;
  worker?.terminate(); clearTimeout(timer);
  const current = new Worker(new URL("worker.js", import.meta.url));
  worker = current;
  status.textContent = "Loading…";
  timer = setTimeout(() => fail("The engine did not load within 30 seconds. Edit the filter to retry."), 30000);
  current.onmessage = ({ data }) => {
    if (current !== worker) return;
    if (data.type === "ready") {
      ready = true; end(); status.textContent = "Ready";
      if (runAfterReady) { runAfterReady = false; execute(); }
    }
    if (data.type === "fatal") fail(data.message);
    if (data.type === "result" && data.id === active) {
      end();
      const response = data.response;
      if (!response.ok) {
        status.textContent = "Could not filter records";
        const error = response.error;
        const location = error.record ? `Record ${error.record}: ` : error.line ? `Line ${error.line}, column ${error.column}: ` : "";
        result.textContent = error.diagnostics?.length
          ? error.diagnostics.map(item => `Line ${item.line}, column ${item.column}: ${item.message}`).join("\n")
          : location + error.message;
        result.dataset.state = "error";
        showHighlights(error);
        return;
      }
      const batch = response.batch;
      status.textContent = `${batch.filtered.length} of ${batch.total} records`;
      // Rows remain raw text all the way through WASM, preserving int64,
      // float spelling and signed zero. Never parse/stringify user records.
      result.textContent = batch.filtered.length ? batch.filtered.join("\n") : "No records match this filter.";
      result.dataset.state = batch.filtered.length ? "records" : "empty";
      [batch.timings.compile_ms, batch.timings.build_ms, batch.timings.evaluate_ms].forEach((value, i) => {
        times[i].textContent = `${value.toFixed(value < 1 ? 3 : 2)} ms`;
      });
    }
  };
  current.onerror = event => { if (current === worker) fail(event.message || "The worker stopped unexpectedly."); };
  current.postMessage({ type: "init" });
}
function fail(message) {
  clearTimeout(debounceTimer);
  worker?.terminate(); worker = undefined; ready = false; runAfterReady = false;
  end(); resetOutput(message); result.dataset.state = "error"; status.textContent = "Stopped";
}
function execute() {
  if (!ready) { runAfterReady = true; return; }
  const rows = record.value.split(/\r?\n/).filter(line => line.trim() !== "");
  const request = `{"query":${JSON.stringify(query.value)},"schema":${schema.value},"records":[${rows.join(",")}]}`;
  active = ++sequence;
  stop.hidden = false; status.textContent = "Filtering…";
  resetOutput("Filtering records…");
  timer = setTimeout(() => fail("Filtering stopped after 5 seconds. No partial result was returned."), 5000);
  worker.postMessage({ type: "match", id: active, request });
}
function requestRun() {
  clearTimeout(debounceTimer);
  if (!worker) boot(true); else execute();
}
stop.addEventListener("click", () => fail("Filtering cancelled. No partial result was returned."));
select.addEventListener("change", () => {
  if (active) fail("Filtering cancelled.");
  displayExample();
  requestRun();
});
for (const input of [query, schema, record]) {
  input.addEventListener("input", () => {
    if (active) fail("Filtering cancelled because the input changed.");
    runAfterReady = false;
    resetOutput("Updating results…"); status.textContent = "Updating…";
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(requestRun, 150);
  });

}
window.addEventListener("pagehide", () => { worker?.terminate(); clearTimeout(timer); clearTimeout(debounceTimer); });
displayExample();
boot(true);
