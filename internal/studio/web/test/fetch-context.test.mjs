import test from "node:test";
import assert from "node:assert/strict";
import { API } from "../assets/core.mjs";

function globalFetchContract() {
  let calls = 0;
  return {
    fetcher: function (url, options) {
      if (this !== globalThis) throw new TypeError("Illegal invocation");
      calls++;
      assert.equal(url.pathname, "/_docs/api/v1/session");
      assert.equal(options.credentials, "same-origin");
      assert.equal(options.redirect, "error");
      return Promise.resolve(new Response(JSON.stringify({ user: null, setupRequired: true }), { headers: { "Content-Type": "application/json" } }));
    },
    get calls() { return calls; },
  };
}

test("default native-style fetch retains its global receiver when reading the initial session", async () => {
  const original = globalThis.fetch;
  const contract = globalFetchContract();
  globalThis.fetch = contract.fetcher;
  try {
    const api = new API("http://localhost:8080/_docs/api/v1/");
    assert.deepEqual(await api.request("session"), { user: null, setupRequired: true });
    assert.equal(contract.calls, 1);
    assert.equal(api.pending.size, 0);
  } finally {
    globalThis.fetch = original;
  }
});

test("a supplied native-style fetch does not receive the API instance as its receiver", async () => {
  const contract = globalFetchContract();
  const api = new API("http://localhost:8080/_docs/api/v1/", contract.fetcher);
  assert.deepEqual(await api.request("session"), { user: null, setupRequired: true });
  assert.equal(contract.calls, 1);
  assert.equal(api.pending.size, 0);
});
