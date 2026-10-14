import assert from "node:assert/strict";
import { createServer } from "node:http";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { Worker } from "node:worker_threads";

const root = process.argv[2];
const requests = [];
const server = createServer((request, response) => {
  requests.push({ path: request.url, method: request.method });
  try { response.end(readFileSync(join(root, new URL(request.url, "http://localhost").pathname))); }
  catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}/playground/worker.js`;
let worker;
function message(data) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { cleanup(); reject(new Error("Worker timed out")); }, 10000);
    const received = result => { cleanup(); resolve(result); };
    const failed = error => { cleanup(); reject(error); };
    const cleanup = () => { clearTimeout(timer); worker.off("message", received); worker.off("error", failed); };
    worker.once("message", received); worker.once("error", failed); worker.postMessage(data);
  });
}
async function start() {
  worker = new Worker(new URL("./playground-worker-host.cjs", import.meta.url), { workerData: { root, url } });
  const ready = await message({ type: "init" });
  assert.equal(ready.type, "ready");
  assert.equal(ready.sqlExamples.length, 5);
  return ready.sqlExamples;
}
try {
  const sqlExamples = await start();
  const schema = [{ name: "age", type: "int" }];
  let result = await message({ type: "match", id: 1, request: JSON.stringify({ schema, query: "age>18", record: { age: 20 } }) });
  assert.deepEqual(result, { type: "result", id: 1, response: { ok: true, matched: true } });
  result = await message({ type: "match", id: 2, request: JSON.stringify({ schema, query: "age>18", record: { age: 12 } }) });
  assert.equal(result.response.matched, false);
  result = await message({ type: "match", id: 3, request: JSON.stringify({ schema, query: "age>", record: { age: 20 } }) });
  assert.equal(result.response.error.stage, "compile");
  assert.equal(result.response.matched, undefined);
  result = await message({ type: "match", id: 4, request: '{"schema":[{"name":"n","type":"int"}],"query":"n=9007199254740993","record":{"n":9007199254740993}}' });
  assert.equal(result.response.matched, true);
  result = await message({ type: "match", id: 9, request: JSON.stringify({ mode: "sql", query: sqlExamples[0].query,
    configuration: JSON.parse(sqlExamples[0].configuration) }) });
  assert.equal(result.response.ok, true);
  assert.ok(result.response.sql.where.includes("people"));
  assert.ok(Array.isArray(result.response.sql.args));
  result = await message({ type: "match", id: 8, request: '{"schema":[{"name":"n","type":"int|float"}],"query":"n:[float]:exists | n=9007199254740993","records":[{"n":12},{"n":9007199254740993},{"n":1.0},{"n":-0.0}]}' });
  assert.deepEqual(result.response.batch.filtered, ['{"n":9007199254740993}', '{"n":1.0}', '{"n":-0.0}']);
  assert.equal(result.response.batch.total, 4);
  for (const measured of Object.values(result.response.batch.timings)) assert.ok(Number.isFinite(measured) && measured >= 0);
  // Kill actual WASM work, then start a fresh runtime rather than reusing state.
  const fields = [{ name: "age", type: "int" }, ...Array.from({ length: 8000 }, (_, i) => ({ name: `f${i}`, type: "int", optional: true }))];
  worker.postMessage({ type: "match", id: 5, request: JSON.stringify({ schema: fields, query: "age>18", record: { age: 20 } }) });
  await new Promise(resolve => setTimeout(resolve, 10));
  await worker.terminate();
  await start();
  result = await message({ type: "match", id: 6, request: JSON.stringify({ schema, query: "age>18", record: { age: 20 } }) });
  assert.equal(result.response.matched, true);
  result = await message({ type: "match", id: 7, request: " ".repeat(1048577) });
  assert.equal(result.type, "fatal");
  assert.ok(requests.length > 0);
  assert.ok(requests.every(r => r.method === "GET" && /^\/playground\/(manifest\.json|hff\.[a-f0-9]{64}\.wasm)$/.test(r.path)));
  console.log("Shipped worker: load, match, errors, integer precision, terminate/restart, and admission passed");
} finally { await worker?.terminate(); await new Promise(resolve => server.close(resolve)); }
