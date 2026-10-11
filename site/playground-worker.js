// Classic worker: wasm_exec.js installs Go through importScripts.
let bridge;
let starting;
async function start() {
  const response = await fetch(new URL("manifest.json", self.location.href), { cache: "no-cache" });
  if (!response.ok) throw new Error(`Runtime manifest: HTTP ${response.status}`);
  const manifest = await response.json();
  // The manifest is build-generated, but still constrain asset names to this directory.
  if (!/^hff\.[a-f0-9]{64}\.wasm$/.test(manifest.wasm) || !/^wasm_exec\.[a-f0-9]{64}\.js$/.test(manifest.runtime)) throw new Error("Invalid runtime manifest");
  importScripts(new URL(manifest.runtime, self.location.href).href);
  const responseWASM = await fetch(new URL(manifest.wasm, self.location.href));
  if (!responseWASM.ok) throw new Error(`WASM module: HTTP ${responseWASM.status}`);
  const go = new Go();
  const { instance } = await WebAssembly.instantiate(await responseWASM.arrayBuffer(), go.importObject);
  const ready = new Promise(resolve => { self.hffPlaygroundReady = resolve; });
  const exited = go.run(instance).then(() => { throw new Error("WASM engine exited"); });
  await Promise.race([ready, exited]);
  bridge = self.hffPlayground;
  // An unexpected Go exit after readiness invalidates the worker.
  exited.catch(error => self.postMessage({ type: "fatal", message: error.message }));
}
self.onmessage = async ({ data }) => {
  try {
    starting ??= start();
    await starting;
    if (data.type === "init") { self.postMessage({ type: "ready" }); return; }
    if (data.type !== "match" || typeof data.request !== "string" || !Number.isSafeInteger(data.id)) throw new Error("Invalid playground message");
    if (new TextEncoder().encode(data.request).length > 1048576) throw new Error("Playground request exceeds 1 MiB");
    self.postMessage({ type: "result", id: data.id, response: JSON.parse(bridge.match(data.request)) });
  } catch (error) { self.postMessage({ type: "fatal", message: String(error.message || error) }); }
};
