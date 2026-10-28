import test from "node:test";
import assert from "node:assert/strict";
import { storageRequest, storageResult, canManageStorage, renderStorage } from "../assets/storage.mjs";

const snapshot = () => ({ settings: { revision: "settings-cas", backend: "git", pullRemote: "origin", remotes: [{ name: "origin", url: "git@example.com:docs.git", push: true }] }, projectRevision: "project-cas", sync: { backend: "git" }, remotes: [] });
const draft = (changes = {}) => ({ backend: "git", pullRemote: "origin", remotes: [{ name: "origin", url: "git@example.com:docs.git", push: true }], ...changes });

test("storage renderer denies non-root capabilities before creating DOM or issuing a GET", async () => {
  const denied = [null, { role: "viewer" }, { role: "editor" }, { role: "admin", disabled: true }, { role: "admin", grants: { read: ["docs"], write: ["."] } }, { role: "admin", grants: { read: ["."], write: ["docs"] } }, { role: "admin", grants: { read: [], write: [] } }];
  let requests = 0;
  for (const user of denied) {
    assert.equal(canManageStorage(user), false);
    await assert.rejects(renderStorage({ user, api: { request() { requests++; } } }), error => error.status === 403);
  }
  assert.equal(requests, 0);
  assert.equal(canManageStorage({ role: "admin" }), true);
  assert.equal(canManageStorage({ role: "admin", grants: { read: ["."], write: ["."] } }), true);
});

test("storage snapshot requires independent revisions and strips response-only configuration metadata", () => {
  const input = snapshot(); input.settings.serverOnly = "never display or send this";
  input.settings.remotes[0].serverOnly = "hidden metadata";
  const result = storageResult(input);
  assert.equal(Object.hasOwn(result.settings, "serverOnly"), false);
  assert.equal(Object.hasOwn(result.settings.remotes[0], "serverOnly"), false);
  const request = storageRequest(result, draft());
  assert.deepEqual(request, { revision: "settings-cas", baseRevision: "project-cas", backend: "git", pullRemote: "origin", remotes: [{ name: "origin", url: "git@example.com:docs.git", push: true }] });
  for (const invalid of [null, {}, { ...input, projectRevision: "" }, { ...input, settings: { ...input.settings, revision: "" } }, { ...input, remotes: null }, { ...input, settings: { ...input.settings, remotes: [{ name: "origin", url: "x" }] } }]) assert.throws(() => storageResult(invalid), /incomplete/);
});

test("storage accepts SSH URIs, explicit-user SCP, absolute local paths and hostless absolute file URLs", () => {
  for (const url of ["git@example.com:team/docs.git", "ssh://git@example.com:2222/team/docs.git", "ssh://example.com/team/docs.git", "/backups/docs.git", "file:///backups/docs.git"]) {
    assert.equal(storageRequest(snapshot(), draft({ remotes: [{ name: "origin", url, push: true }] })).remotes[0].url, url);
  }
  for (const url of ["https://example.com/docs.git", "http://example.com/docs.git", "ssh://git:password@example.com/docs.git", "file://localhost/backups/docs.git", "file://foreign/backups/docs.git", "file:relative.git", "./relative.git", "../outside.git", "relative.git", "-option", "git@host:docs.git?secret=value", "git@host:docs.git#fragment", "git@host:docs.git\n", "/backups/\u0000docs.git", "/" + "a".repeat(2048)]) {
    assert.throws(() => storageRequest(snapshot(), draft({ remotes: [{ name: "origin", url, push: true }] })), { name: "APIError" }, url);
  }
});

test("storage bounds connections and applies case-folded portable names and an explicit single pull authority", () => {
  for (const name of ["origin", "Origin_2", "remote-8", "r".repeat(64)]) assert.equal(storageRequest(snapshot(), draft({ pullRemote: name, remotes: [{ name, url: "/backup.git", push: false }] })).pullRemote, name);
  for (const name of ["", ".", "origin.backup", "../origin", "-option", "r".repeat(65), "CON", "prn", "aux", "nul", "COM1", "LPT9", "русский"]) assert.throws(() => storageRequest(snapshot(), draft({ pullRemote: "", remotes: [{ name, url: "/backup.git", push: true }] })), { name: "APIError" }, name);
  const remotes = Array.from({ length: 8 }, (_, index) => ({ name: "remote" + index, url: "/backup" + index + ".git", push: index % 2 === 0 }));
  assert.equal(storageRequest(snapshot(), draft({ remotes, pullRemote: "remote7" })).remotes.length, 8);
  assert.throws(() => storageRequest(snapshot(), draft({ remotes: [...remotes, { name: "ninth", url: "/ninth.git", push: true }], pullRemote: "" })), /eight/);
  assert.throws(() => storageRequest(snapshot(), draft({ remotes: [{ name: "origin", url: "/a.git", push: true }, { name: "ORIGIN", url: "/b.git", push: true }] })), /unique/);
  assert.throws(() => storageRequest(snapshot(), draft({ pullRemote: "missing" })), /pull source/);
  assert.throws(() => storageRequest(snapshot(), draft({ remotes: [{ name: "origin", url: "/a.git" }] })), /explicit push/);
});

test("Git enrollment includes only its initial branch and cannot downgrade or alter caller state", () => {
  const local = { ...snapshot(), settings: { revision: "local-cas", backend: "local", pullRemote: "", remotes: [] } };
  assert.equal(storageRequest(local, draft()).initialBranch, "main");
  assert.equal(storageRequest(local, draft({ initialBranch: "docs/start" })).initialBranch, "docs/start");
  assert.equal(storageRequest(local, draft({ initialBranch: "я".repeat(64) })).initialBranch, "я".repeat(64));
  assert.throws(() => storageRequest(local, draft({ initialBranch: "я".repeat(65) })), /128 UTF-8 bytes/);
  assert.throws(() => storageRequest(snapshot(), draft({ backend: "local", pullRemote: "", remotes: [] })), /not supported/);
  assert.throws(() => storageRequest(local, draft({ backend: "local" })), /Choose Git/);
  assert.throws(() => storageRequest(local, draft({ backend: "network" })), /supported/);
  assert.deepEqual(storageRequest(local, draft({ backend: "local", pullRemote: "", remotes: [] })), { revision: "local-cas", baseRevision: "project-cas", backend: "local", pullRemote: "", remotes: [] });
  assert.equal(local.settings.backend, "local"); assert.deepEqual(local.settings.remotes, []);
});
