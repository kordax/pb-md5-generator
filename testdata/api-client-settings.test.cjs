"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const http = require("node:http");
const { execFile } = require("node:child_process");
const { promisify } = require("node:util");
const client = require("../internal/site/api-client.js");

function fixture(server = "http://localhost:8090") {
  return { api: { protocol: "http", servers: [{ url: server }], securitySchemes: [{ id: "bearer", type: "http", scheme: "bearer" }] }, operation: { id: "create", method: "POST", path: "/records", security: [{ bearer: [] }], requestBody: { required: true, content: [{ name: "application/json" }, { name: "text/plain" }] } } };
}
function build(overrides = {}, server) {
  const { api, operation } = fixture(server);
  return client.buildRequest(api, operation, { body: '{"message":"example"}', mediaType: "application/json", credentials: { bearer: { value: "test-token-value" } }, ...overrides });
}

test("custom headers retain exact values and reject collisions, forbidden names and resource overflow", () => {
  const request = build({ headers: [["X-Trace", "trace-7"], ["X-Custom-Secret", "hidden-value"]] });
  assert.equal(request.headers["X-Trace"], "trace-7");
  assert.ok(!request.preview.includes("hidden-value"));
  for (const headers of [
    [["Host", "elsewhere.test"]], [["Cookie", "sid=value"]], [["Sec-Fetch-Site", "same-origin"]],
    [["Authorization", "override"]], [["Content-Type", "text/plain"]], [["X-Trace", "a"], ["x-trace", "b"]],
    [["X-Trace", "a\r\nCookie: sid=value"]], [["bad name", "value"]], [["X-Trace"]],
    Array.from({ length: 33 }, (_, index) => ["X-Field-" + index, "value"]), { "X-Trace": "value" },
  ]) assert.throws(() => build({ headers }));
});

test("cURL preview redacts credentials and body while explicit copy preserves entered bytes", () => {
  const body = '{"message":"O\'Reilly $(printf unsafe) `printf unsafe`\\nline"}';
  const request = build({ body, headers: [["X-Trace", "quote ' and $USER"]] });
  const preview = client.curlRequest(request);
  assert.match(preview, /curl --include/);
  assert.ok(!preview.includes("test-token-value"));
  assert.ok(!preview.includes("O'Reilly"));
  assert.match(preview, /body bytes redacted/);
  const copied = client.curlRequest(request, { redact: false });
  assert.match(copied, /test-token-value/);
  assert.ok(copied.includes("O'\\''Reilly"));
  assert.ok(copied.includes("$(printf unsafe)"));
  assert.ok(copied.includes("quote '\\'' and $USER"));
  const cookies = client.curlRequest({ ...request, credentials: "include" }, { redact: false });
  assert.match(cookies, /Browser-managed cookies are not included/);
  const direct = client.curlRequest({ url: "http://localhost:8090/ping?token=direct-query-secret", method: "GET", headers: { Authorization: "Bearer direct-header-secret" } });
  assert.ok(!direct.includes("direct-query-secret"));
  assert.ok(!direct.includes("direct-header-secret"));
});

test("site client settings accept only portable bounded request limits", () => {
  assert.deepEqual(client.clientSettings(), { timeoutMs: 30000, maxBytes: 1048576, showCurl: true });
  assert.deepEqual(client.clientSettings({ requestTimeoutMs: "1500", maxResponseBytes: "4096", showCurl: "false" }), { timeoutMs: 1500, maxBytes: 4096, showCurl: false });
  for (const input of [{ requestTimeoutMs: 999 }, { requestTimeoutMs: 30001 }, { requestTimeoutMs: "Infinity" }, { requestTimeoutMs: 1500.5 }, { maxResponseBytes: 1023 }, { maxResponseBytes: 1048577 }, { maxResponseBytes: "invalid" }]) assert.throws(() => client.clientSettings(input), /Invalid documentation API client limits/);
});

test("copied cURL reaches only the loopback server and preserves shell metacharacters as request data", { skip: process.platform === "win32" ? "POSIX shell quoting is executed on Unix" : false }, async t => {
  const received = [];
  const server = http.createServer(async (request, response) => {
    const chunks = []; for await (const chunk of request) chunks.push(chunk);
    received.push({ method: request.method, path: request.url, headers: request.headers, body: Buffer.concat(chunks).toString("utf8") });
    response.writeHead(200, { "Content-Type": "application/json" }); response.end('{"ok":true}');
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  t.after(() => new Promise(resolve => server.close(resolve)));
  const body = JSON.stringify({ message: "O'Reilly $(printf changed) `printf changed`\nnext line" });
  const request = build({ body, headers: [["X-Trace", "trace ' $USER `printf changed`"]] }, "http://127.0.0.1:" + server.address().port);
  const command = client.curlRequest(request, { redact: false });
  const execution = { timeout: 5000, maxBuffer: 65536, env: { ...process.env, NO_PROXY: "127.0.0.1,localhost", no_proxy: "127.0.0.1,localhost" } };
  const result = await promisify(execFile)("sh", ["-c", command], execution);
  assert.match(result.stdout, /HTTP\/1.1 200/);
  assert.match(result.stdout, /"ok":true/);
  assert.equal(received.length, 1);
  assert.equal(received[0].method, "POST");
  assert.equal(received[0].path, "/records");
  assert.equal(received[0].body, body);
  assert.equal(received[0].headers["x-trace"], "trace ' $USER `printf changed`");
  assert.equal(received[0].headers.authorization, "Bearer test-token-value");
  const atBody = "@must-not-be-read-from-a-local-file";
  const atRequest = build({ body: atBody, mediaType: "text/plain" }, "http://127.0.0.1:" + server.address().port);
  await promisify(execFile)("sh", ["-c", client.curlRequest(atRequest, { redact: false })], execution);
  assert.equal(received.length, 2);
  assert.equal(received[1].body, atBody, "a leading @ is literal body content, never a local filename");
});
