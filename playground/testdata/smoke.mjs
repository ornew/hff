import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { spawnSync } from "node:child_process";
import { webcrypto } from "node:crypto";

const [wasm, runtime, cli] = process.argv.slice(2);
globalThis.crypto ??= webcrypto;
createRequire(import.meta.url)(runtime);
const go = new Go();
const { instance } = await WebAssembly.instantiate(readFileSync(wasm), go.importObject);
go.run(instance);
assert.equal(typeof hffPlayground.match, "function");

// Raw text is intentional: JSON.parse/stringify would round the large integer.
const cases = [
  ['{"schema":[{"name":"age","type":"int"}],"query":"age>=18","record":{"age":20}}', true],
  ['{"schema":[{"name":"age","type":"int"}],"query":"age>=18","record":{"age":12}}', false],
  ['{"schema":[{"name":"n","type":"int"}],"query":"n=9007199254740993","record":{"n":9007199254740993}}', true],
  [String.raw`{"schema":[{"name":"n","type":"float"}],"query":"n:tostring=\"-0.0\"","record":{"n":-0.0}}`, true],
  ['{"schema":[{"name":"items","type":"list[int]"}],"query":"items:any(x=>x>10)","record":{"items":[1,11]}}', true],
  ['{"schema":[{"name":"age","type":"int"}],"query":"age>","record":{"age":20}}', "compile"],
  ['{"schema":[{"name":"age","type":"int"}],"query":"age","record":{"age":"20"}}', "match"],
  ['{"schema":[{"name":"age","type":"int"}],"query":"age>18","record":{"age":20},"without":["operator:comparison"]}', "compile"],
  ['{"schema":[],"query":"","record":{"x":"\\ud800"}}', "request"],
  [JSON.stringify({ schema: [{ name: "x", type: "any" }], query: "(".repeat(64) + "x" + ")".repeat(64), record: { x: true } }), true],
  [JSON.stringify({ schema: [{ name: "x", type: "any" }], query: "(".repeat(65) + "x" + ")".repeat(65), record: { x: true } }), "compile"],
  ['{"schema":[{"name":"x","type":"any"}],"query":"x:exists","record":{"x":' + "[".repeat(60) + "0" + "]".repeat(60) + '}}', true],
];
for (const [raw, expected] of cases) {
  const native = spawnSync(cli, [], { input: raw, encoding: "utf8", timeout: 10000 });
  assert.equal(native.status, 0, native.stderr);
  const actual = JSON.parse(hffPlayground.match(raw));
  assert.deepEqual(actual, JSON.parse(native.stdout));
  if (typeof expected === "boolean") { assert.equal(actual.ok, true, raw + JSON.stringify(actual)); assert.equal(actual.matched, expected, raw); }
  else { assert.equal(actual.ok, false); assert.equal(actual.error.stage, expected); assert.equal(actual.matched, undefined); }
}
for (const raw of [" ".repeat((1 << 20) + 1), "é".repeat(1 << 20)]) {
  assert.equal(JSON.parse(hffPlayground.match(raw)).error.code, "limit");
}
for (const args of [[], [null], [{}]]) {
  assert.equal(JSON.parse(hffPlayground.match(...args)).ok, false);
}
console.log(`${cases.length} native/WASM bridge cases and 5 JS admission checks passed`);
process.exit(0);
