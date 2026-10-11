const examples = {
  people: { query: 'age>=18 name', schema: [{ name: "name", type: "string" }, { name: "age", type: "int" }], record: '{"name":"Alice","age":20}' },
  labels: { query: 'labels.app="auth" labels.env:in("prod","staging")', schema: [{ name: "labels", type: "map[string]" }], record: '{"labels":{"app":"auth","env":"prod","team":"identity"}}' },
  orders: { query: 'items:any(item=>item.price>100 item.active)', schema: [{ name: "items", type: "list[map[any]]" }], record: '{"items":[{"price":45,"active":true},{"price":120,"active":true}]}' },
};
const query = document.querySelector("#playground-query");
const schema = document.querySelector("#playground-schema");
const record = document.querySelector("#playground-record");
const select = document.querySelector("#playground-example");
const run = document.querySelector("#playground-run");
const stop = document.querySelector("#playground-stop");
const status = document.querySelector("#playground-status");
const result = document.querySelector("#playground-result");
let worker, timer, sequence = 0, active = 0, runAfterReady = false;
function displayExample() {
  const example = examples[select.value];
  query.value = example.query;
  schema.value = JSON.stringify(example.schema, null, 2);
  // Format curated records only. User input is never parsed and reserialized.
  record.value = JSON.stringify(JSON.parse(example.record), null, 2);
  result.textContent = "";
}
function end() { clearTimeout(timer); active = 0; run.disabled = false; stop.disabled = true; }
function boot(thenRun = false) {
  runAfterReady = thenRun;
  worker?.terminate();
  const current = new Worker(new URL("worker.js", import.meta.url));
  worker = current;
  run.disabled = true;
  status.textContent = "Loading the local WASM engine…";
  timer = setTimeout(() => fail("The engine did not load within 30 seconds. Run again to retry."), 30000);
  current.onmessage = ({ data }) => {
    if (current !== worker) return;
    if (data.type === "ready") {
      end(); status.textContent = "Ready. Evaluation stays in this browser.";
      if (runAfterReady) { runAfterReady = false; execute(); }
    }
    if (data.type === "fatal") fail(data.message);
    if (data.type === "result" && data.id === active) {
      end();
      const response = data.response;
      status.textContent = response.ok ? (response.matched ? "Matched" : "Not matched") : "Evaluation did not complete";
      result.textContent = response.ok ? JSON.stringify({ matched: response.matched }, null, 2) : JSON.stringify(response.error, null, 2);
    }
  };
  current.onerror = event => { if (current === worker) fail(event.message || "The worker stopped unexpectedly."); };
  current.postMessage({ type: "init" });
}
function fail(message) { worker?.terminate(); worker = undefined; runAfterReady = false; end(); status.textContent = "Engine stopped"; result.textContent = message; }
function execute() {
  // JSON texts are inserted verbatim so large integers and float spelling survive.
  const request = `{"query":${JSON.stringify(query.value)},"schema":${schema.value},"record":${record.value}}`;
  active = ++sequence;
  run.disabled = true; stop.disabled = false; status.textContent = "Evaluating…"; result.textContent = "";
  timer = setTimeout(() => fail("Evaluation stopped after 5 seconds. No match result was produced."), 5000);
  worker.postMessage({ type: "match", id: active, request });
}
run.addEventListener("click", () => { if (!worker) boot(true); else execute(); });
stop.addEventListener("click", () => { fail("Evaluation cancelled. No match result was produced."); });
select.addEventListener("change", () => { if (active) fail("Evaluation cancelled."); displayExample(); });
window.addEventListener("pagehide", () => { worker?.terminate(); clearTimeout(timer); });
displayExample();
boot();
