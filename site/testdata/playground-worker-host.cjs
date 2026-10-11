// Node adapter for testing the shipped classic Worker source without rewriting it.
const { parentPort, workerData } = require("node:worker_threads");
const { readFileSync } = require("node:fs");
const { join } = require("node:path");
const { runInThisContext } = require("node:vm");
globalThis.self = globalThis;
globalThis.crypto ??= require("node:crypto").webcrypto;
globalThis.location = { href: workerData.url };
self.postMessage = message => parentPort.postMessage(message);
self.importScripts = url => require(join(workerData.root, new URL(url).pathname));
runInThisContext(readFileSync(join(workerData.root, "playground/worker.js"), "utf8"));
parentPort.on("message", data => self.onmessage({ data }));
