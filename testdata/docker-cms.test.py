#!/usr/bin/env python3
"""Exercise the CMS image with isolated volumes and memory-only HTTP credentials."""

import argparse
import hashlib
import http.cookies
import io
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import stat
import subprocess
import tarfile
import tempfile


ROOT = Path(__file__).resolve().parents[1]


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def run(arguments, *, env=None, input_data=None, expected=0, timeout=180):
    result = subprocess.run(arguments, cwd=ROOT, env=env, input=input_data,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
    if expected is not None and result.returncode != expected:
        # Commands never carry passwords or tokens in arguments. Captured HTTP
        # output and headers are deliberately excluded from failure messages.
        detail = result.stderr.decode(errors="replace")[-4096:] if "restore" in arguments else ""
        raise RuntimeError(f"Command failed ({result.returncode}): {' '.join(arguments[:5])}\n{detail}")
    return result


class Client:
    def __init__(self, origin, prefix=""):
        self.origin = origin
        self.prefix = prefix
        self.cookies = {}
        self.csrf = None
        self.location = None
        self.response_headers = {}

    def request(self, path, method="GET", body=None, expected=200, parse=True, headers=None):
        require(path.startswith("/") and not path.startswith("//"), "HTTP path must be local")
        lines = [f"Origin: {self.origin}"]
        if self.cookies:
            lines.append("Cookie: " + "; ".join(f"{key}={value}" for key, value in self.cookies.items()))
        if self.csrf:
            lines.append(f"X-CSRF-Token: {self.csrf}")
        payload = None
        if body is not None:
            if isinstance(body, bytes):
                lines.append("Content-Type: application/octet-stream")
                payload = body
            else:
                lines.append("Content-Type: application/json")
                payload = json.dumps(body).encode()
        for name, value in (headers or {}).items():
            require(not any(char in name + value for char in "\r\n"), "Invalid test request header")
            lines.append(f"{name}: {value}")
        # Anonymous pipes keep cookies and CSRF headers out of files and argv.
        request_read, request_write = os.pipe()
        response_read, response_write = os.pipe()
        os.write(request_write, ("\n".join(lines) + "\n").encode())
        os.close(request_write)
        arguments = ["curl", "--silent", "--show-error", "--noproxy", "*", "--max-time", "15",
                     "--request", method, "--header", f"@/dev/fd/{request_read}",
                     "--dump-header", f"/dev/fd/{response_write}", "--write-out", "\n%{http_code}"]
        if payload is not None:
            arguments += ["--data-binary", "@-"]
        arguments.append(self.origin + self.prefix + path)
        try:
            result = subprocess.run(arguments, input=payload, stdout=subprocess.PIPE,
                                    stderr=subprocess.PIPE, pass_fds=(request_read, response_write), timeout=20)
        finally:
            os.close(request_read)
            os.close(response_write)
        headers = bytearray()
        try:
            while True:
                part = os.read(response_read, 8192)
                if not part:
                    break
                headers.extend(part)
                require(len(headers) <= 65536, "HTTP response headers exceeded their limit")
        finally:
            os.close(response_read)
        require(result.returncode == 0, "Loopback HTTP request failed")
        data, separator, code = result.stdout.rpartition(b"\n")
        require(separator and code.isdigit(), "Missing HTTP response status")
        require(int(code) == expected, f"Unexpected HTTP status for {method} {path}: {int(code)}")
        self.location = None
        self.response_headers = {}
        for line in headers.decode("iso-8859-1").splitlines():
            name, _, value = line.partition(":")
            if name.lower() in {"etag", "x-base-revision", "x-source-revision", "content-type", "content-disposition",
                                "content-security-policy", "x-content-type-options"}:
                self.response_headers[name.lower()] = value.strip()
            if line.lower().startswith("location:"):
                self.location = line.partition(":")[2].strip()
            if line.lower().startswith("set-cookie:"):
                cookie = http.cookies.SimpleCookie()
                cookie.load(line.partition(":")[2].strip())
                for key, morsel in cookie.items():
                    require(morsel["secure"] and morsel["httponly"], "Session cookie lacks required flags")
                    self.cookies[key] = morsel.value
        if not parse:
            return data
        value = json.loads(data) if data else None
        if isinstance(value, dict) and value.get("csrfToken"):
            self.csrf = value["csrfToken"]
        return value


def exercise_source_and_folder_deletion(client):
    path = "/_docs/api/v1/files/content?path=examples/client.js"
    original = b"export const message = '<script>literal example</script>';\n"
    updated = original + b"export const version = 2;\n"
    project = client.request("/_docs/api/v1/documents")
    base = project["revision"]
    saved = client.request(path, "PUT", original, headers={"If-Match": f'"{base}"'})
    require(saved.get("published") is False and saved.get("draft"), "Code upload must start as a draft")
    draft = saved["draft"]
    require(client.request(path + "&draftId=" + draft["id"], parse=False) == original,
            "New unpublished source draft could not be reopened")
    require(client.response_headers.get("etag") == f'"{draft["revision"]}"', "Draft ETag is not its current revision")
    require(client.response_headers.get("x-base-revision") == base, "Draft response lost its original project base")
    headers = {"If-Match": f'"{base}"', "X-Draft-ID": draft["id"], "X-Draft-Revision": draft["revision"]}
    saved = client.request(path, "PUT", updated, headers=headers)
    current = saved["draft"]
    require(current["id"] == draft["id"] and current["revision"] != draft["revision"], "Source save did not use draft CAS")
    client.request(path, "PUT", original, expected=409, headers=headers)
    require(client.request(path + "&draftId=" + current["id"], parse=False) == updated,
            "A stale save overwrote the newer source draft")
    settings = client.request("/_docs/api/v1/settings")
    settings.pop("sync", None)
    settings["footer"] = "Source rebase verification"
    client.request("/_docs/api/v1/settings", "PUT", settings)
    latest = client.request("/_docs/api/v1/documents")["revision"]
    require(latest != base, "Independent publication did not advance the project revision")
    resave_headers = {"If-Match": f'"{base}"', "X-Draft-ID": current["id"], "X-Draft-Revision": current["revision"]}
    current = client.request(path, "PUT", updated, headers=resave_headers)["draft"]
    require(current["baseRevision"] == base, "Ordinary source save silently changed its stored base")
    rebase_headers = {"If-Match": f'"{base}"', "X-Draft-ID": current["id"], "X-Draft-Revision": current["revision"]}
    client.request("/_docs/api/v1/files/rebase?path=examples/client.js", "POST", updated, expected=409, headers=rebase_headers)
    rebase_headers["If-Match"] = f'"{latest}"'
    rebased = client.request("/_docs/api/v1/files/rebase?path=examples/client.js", "POST", updated, headers=rebase_headers)
    require(rebased.get("published") is False and rebased["draft"]["id"] == current["id"]
            and rebased["draft"]["baseRevision"] == latest, "Reviewed source rebase did not retain its owned draft")
    current = rebased["draft"]
    published = client.request(f"/_docs/api/v1/drafts/{current['id']}/publish", "POST",
                               {"revision": current["revision"], "push": True, "message": "docs: add literal code example"})
    require(published.get("published") is True, "Code example publication failed")
    require(client.request(path, parse=False) == updated, "Published code bytes changed")
    require(client.response_headers.get("content-type") == "application/octet-stream", "Code source has an executable MIME type")
    require(client.response_headers.get("content-disposition", "").startswith("attachment;"), "Code source is not a download")
    require(client.response_headers.get("x-content-type-options") == "nosniff", "Code download allows MIME sniffing")
    require("sandbox" in client.response_headers.get("content-security-policy", ""), "Code download lacks sandbox policy")
    client.request("/examples/client.js", expected=404, parse=False)
    history = client.request("/_docs/api/v1/files/history?path=examples/client.js")
    require(history["path"] == "examples/client.js" and history["revisions"], "Source history is missing")
    restore_id = next((entry["id"] for entry in history["revisions"] if not entry["deleted"] and entry["size"] == len(updated)), None)
    require(restore_id, "Source history omitted the retained code bytes")
    for entry in history["revisions"]:
        require(entry["path"] == history["path"] and not ({"actor", "email", "branch", "documentId"} & entry.keys()),
                "Source history exposed unrelated paths or private metadata")
    settings = client.request("/_docs/api/v1/settings")
    settings.pop("sync", None)
    settings["publishing"]["autoPublish"] = True
    client.request("/_docs/api/v1/settings", "PUT", settings)
    project = client.request("/_docs/api/v1/documents")
    plan = client.request("/_docs/api/v1/files/delete-preview", "POST", {"path": "examples", "revision": project["revision"]})
    require(plan.get("fileCount") == 1 and plan.get("directoryCount") == 1 and not plan.get("truncated"),
            "Folder deletion preview has an incorrect inventory")
    staged = client.request("/_docs/api/v1/files/delete-tree", "POST",
                            {"path": plan["path"], "revision": plan["revision"], "planRevision": plan["planRevision"]})
    require(staged.get("published") is False and staged.get("draft", {}).get("operation") == "delete-tree",
            "Recursive deletion bypassed explicit draft publication")
    require(client.request(path, parse=False) == updated, "Staging a folder deletion removed live content")
    deletion = staged["draft"]
    result = client.request(f"/_docs/api/v1/drafts/{deletion['id']}/publish", "POST",
                            {"revision": deletion["revision"], "push": True, "message": "docs: remove literal examples"})
    require(result.get("published") is True, "Folder deletion publication failed")
    client.request(path, expected=404, parse=False)
    history = client.request("/_docs/api/v1/files/history?path=examples/client.js")
    restored = client.request("/_docs/api/v1/files/restore", "POST",
                              {"path": "examples/client.js", "historyId": restore_id, "revision": history["revision"]})
    require(restored.get("published") is False and restored.get("draft"), "File restore bypassed explicit publication")
    client.request(path, expected=404, parse=False)
    restored_draft = restored["draft"]
    client.request(f"/_docs/api/v1/drafts/{restored_draft['id']}/publish", "POST",
                   {"revision": restored_draft["revision"], "push": True, "message": "docs: restore literal source"})
    require(client.request(path, parse=False) == updated, "Restored source bytes changed")
    project = client.request("/_docs/api/v1/documents")
    plan = client.request("/_docs/api/v1/files/delete-preview", "POST", {"path": "examples", "revision": project["revision"]})
    final_delete = client.request("/_docs/api/v1/files/delete-tree", "POST",
                                  {"path": plan["path"], "revision": plan["revision"], "planRevision": plan["planRevision"]})["draft"]
    client.request(f"/_docs/api/v1/drafts/{final_delete['id']}/publish", "POST",
                   {"revision": final_delete["revision"], "push": True, "message": "docs: finish restored source verification"})
    client.request(path, expected=404, parse=False)
    settings = client.request("/_docs/api/v1/settings")
    settings.pop("sync", None)
    settings["publishing"]["autoPublish"] = False
    client.request("/_docs/api/v1/settings", "PUT", settings)
    print("Literal drafts, reviewed rebase, exact-path history, file restore and explicit folder deletion passed", flush=True)
    exercise_source_registration(client)


def exercise_source_registration(client):
    path = "/_docs/api/v1/files/content?path=registration/imported.md"
    source = b"\xef\xbb\xbf# Registered source\r\n\r\nExact existing bytes.\r\n"
    revision = client.request("/_docs/api/v1/documents")["revision"]
    uploaded = client.request(path, "PUT", source, headers={"If-Match": f'"{revision}"'})["draft"]
    client.request(f"/_docs/api/v1/drafts/{uploaded['id']}/publish", "POST",
                   {"revision": uploaded["revision"], "push": True, "message": "docs: add source for registration"})
    settings = client.request("/_docs/api/v1/settings")
    settings.pop("sync", None)
    settings["publishing"]["autoPublish"] = True
    client.request("/_docs/api/v1/settings", "PUT", settings)
    require(client.request(path, parse=False) == source, "Registration review changed source bytes")
    revision = client.response_headers["etag"].strip('"')
    source_revision = client.response_headers.get("x-source-revision")
    require(source_revision == hashlib.sha256(source).hexdigest(), "Live source review digest does not match exact bytes")
    metadata = {"path": "registration/imported.md", "revision": revision, "sourceRevision": source_revision,
                "title": "Registered source", "kind": "markdown", "id": "registered-source",
                "outputPath": "registration/imported.html"}
    client.request("/_docs/api/v1/documents/register", "POST", {**metadata, "sourceRevision": "0" * 64}, expected=409)
    registered = client.request("/_docs/api/v1/documents/register", "POST", metadata, expected=201)
    require(registered.get("published") is False and registered.get("document", {}).get("id") == metadata["id"],
            "Registration bypassed explicit publication or lost page identity")
    draft = registered["draft"]
    published = client.request("/_docs/api/v1/documents?published=true")
    require(not any(item["id"] == metadata["id"] for item in published["documents"]),
            "Staging registration changed the live document catalog")
    client.request("/registration/imported.html", expected=404, parse=False)
    preview = client.request("/_docs/api/v1/preview", "POST", {"documentId": metadata["id"], "content": source.decode("utf-8")})
    require("Exact existing bytes." in preview.get("html", ""), "Registration draft could not be previewed")
    require(client.request(path, parse=False) == source, "Staging registration replaced the original source")
    result = client.request(f"/_docs/api/v1/drafts/{draft['id']}/publish", "POST",
                            {"revision": draft["revision"], "push": True, "message": "docs: register existing source"})
    require(result.get("published") is True, "Explicit registration publication failed")
    require(b"Exact existing bytes." in client.request("/registration/imported.html", parse=False),
            "Registered page is missing from the rendered site")
    require(client.request(path, parse=False) == source, "Registration publication changed BOM or line endings")
    metadata["revision"] = client.response_headers["etag"].strip('"')
    client.request("/_docs/api/v1/documents/register", "POST", metadata, expected=409)
    settings = client.request("/_docs/api/v1/settings")
    settings.pop("sync", None)
    settings["publishing"]["autoPublish"] = False
    client.request("/_docs/api/v1/settings", "PUT", settings)
    print("Reviewed source registration, preview and explicit Git publication passed", flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--skip-build", action="store_true", help="test the existing pb-docs-cms:local image")
    parser.add_argument("--skip-static", action="store_true", help="skip the separate static image check")
    options = parser.parse_args()
    require(os.name == "posix", "Run this Docker integration check on Linux or macOS")
    require(shutil.which("docker") and shutil.which("curl"), "Docker Compose and curl are required")
    os.umask(0o077)
    project = f"pb-cms-qa-{os.getpid()}-{secrets.token_hex(3)}"
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    origin = f"http://localhost:{port}"
    env = os.environ.copy()
    env.update({"DOCS_BIND": "127.0.0.1", "DOCS_PORT": str(port), "CMS_PUBLIC_URL": origin,
                "CMS_STORAGE": "git", "CMS_GIT_REMOTE": "origin", "DOCS_CONFIG": "docs.json",
                "CMS_DATA_VOLUME": project + "_data", "CMS_BACKUP_VOLUME": project + "_backups",
                "CMS_BACKUP_FILE": "qa-state.tar.gz"})
    base_env = env.copy()
    volumes = {env["CMS_DATA_VOLUME"], env["CMS_BACKUP_VOLUME"]}
    existing = run(["docker", "volume", "ls", "--filter", f"name={project}", "--format", "{{.Name}}"])
    require(not existing.stdout.strip(), "The generated QA project name is already in use")
    compose_prefix = ["docker", "compose", "-p", project]

    def compose(*arguments, environment=None, input_data=None, expected=0, timeout=180):
        return run(compose_prefix + list(arguments), env=environment or env,
                   input_data=input_data, expected=expected, timeout=timeout)

    def shell(source, *, environment=None, input_data=None):
        return compose("run", "--rm", "--no-deps", "-T", "--entrypoint", "sh", "cms", "-ec", source,
                       environment=environment, input_data=input_data)

    def fresh_env(suffix, *, backup=None, archive="qa-state.tar.gz"):
        selected = base_env.copy()
        selected["CMS_DATA_VOLUME"] = project + "_" + suffix
        selected["CMS_BACKUP_FILE"] = archive
        if backup:
            selected["CMS_BACKUP_VOLUME"] = backup
        volumes.add(selected["CMS_DATA_VOLUME"])
        return selected

    def assert_state(client, draft_text):
        session = client.request("/_docs/api/v1/session")
        require(session.get("user", {}).get("username") == "qa-owner", "Account or session did not survive")
        page = client.request("/", parse=False)
        require(b"Docker publication verification" in page, "Published document did not survive")
        require(draft_text.encode() not in page, "Unpublished draft escaped into the reader")
        client.request("/_docs/api/v1/files/content?path=examples/client.js", expected=404, parse=False)
        client.request("/examples/client.js", expected=404, parse=False)
        drafts = client.request("/_docs/api/v1/drafts")
        require(len(drafts["drafts"]) == 1, "Unpublished draft metadata did not survive")
        document = client.request("/_docs/api/v1/documents/home")
        require(document["content"] == draft_text, "Unpublished draft source did not survive")
        branches = client.request("/_docs/api/v1/branches")
        require(any(branch["name"] == "review" for branch in branches["branches"]), "Git branch did not survive")
        storage = client.request("/_docs/api/v1/storage/configuration")
        require(storage["settings"]["backend"] == "git", "Managed storage choice did not survive")
        require(storage["settings"]["pullRemote"] == "origin", "Pull authority did not survive")
        require(storage["settings"]["remotes"] == [{"name": "origin", "url": "/data/remote.git", "push": True}],
                "Managed connections did not survive")
        settings = client.request("/_docs/api/v1/settings")
        tree = settings.get("navigation", {}).get("tree", {})
        require(tree.get("unlistedPages") == "append" and any(item.get("id") == "getting-started" for item in tree.get("items", [])),
                "Published navigation hierarchy did not survive")

    try:
        if not options.skip_build:
            print("Building CMS image", flush=True)
            compose("build", "cms", timeout=300)
        password = secrets.token_urlsafe(32).encode()
        compose("run", "--rm", "--no-deps", "-T", "cms", "admin-user", "--username", "qa-owner",
                "--display-name", "Docker QA", "--password-stdin", input_data=password)
        shell("mkdir -m 0700 -p /data/content; git init --bare /data/remote.git; "
              "git -C /data/content init -b main; git -C /data/content remote add origin /data/remote.git")
        marker = secrets.token_bytes(32)
        shell("umask 077; cat > /data/ssh/backup-marker", input_data=marker)
        compose("up", "-d", "--wait", "cms")
        client = Client(origin)
        health = client.request("/_docs/health")
        require(health == {"status": "ready"}, "CMS health did not report control-plane readiness")
        client.request("/_docs/api/v1/session", "POST", {"username": "qa-owner", "password": password.decode()})
        password = b""
        require(client.cookies and client.csrf, "Login did not return a session and CSRF token")
        admin = client.request("/_docs/admin/", parse=False)
        require(b"__PB_DOCS_CSP_NONCE__" not in admin, "Admin nonce placeholder was not replaced")
        client.request("/_docs/admin/app.mjs", parse=False)
        for module in ["storage.mjs", "navigation.mjs", "files.mjs"]:
            served_module = client.request(f"/_docs/admin/{module}", parse=False)
            with open(os.path.join(ROOT, "internal/studio/web/assets", module), "rb") as source:
                require(served_module == source.read(), "Packaged editor module differs from the built source")
        storage = client.request("/_docs/api/v1/storage/configuration")
        require(storage["settings"]["backend"] == "git", "Legacy Git startup did not seed managed storage")
        require(len(storage["settings"]["revision"]) == 64 and len(storage["projectRevision"]) == 64,
                "Storage and project revisions must be separate complete tokens")
        require(isinstance(storage["settings"]["remotes"], list) and isinstance(storage["remotes"], list),
                "Connection arrays must not be null")
        print("Health, login, admin assets, managed storage and cookie flags passed", flush=True)

        document = client.request("/_docs/api/v1/documents/home")
        text = "# Docker publication verification\n\nPublished through the real HTTP API.\n"
        client.request("/_docs/api/v1/documents/home", "PUT", {"revision": document["revision"], "content": text})
        drafts = client.request("/_docs/api/v1/drafts")
        require(len(drafts["drafts"]) == 1, "Editor did not retain the saved draft")
        draft = drafts["drafts"][0]
        published = client.request(f"/_docs/api/v1/drafts/{draft['id']}/publish", "POST",
                                   {"revision": draft["revision"], "push": True, "message": "docs: verify Docker publication"})
        require(published.get("published") is True, "Publication did not complete")
        require(published.get("sync", {}).get("backend") == "git", "Publication lost its backend identity")
        require(not published["sync"].get("error"), "Git publication or push failed")
        settings = client.request("/_docs/api/v1/settings")
        settings.pop("sync", None)
        settings["navigation"]["tree"] = {"unlistedPages": "append", "items": [
            {"kind": "group", "id": "getting-started", "title": "Getting started", "collapsed": True,
             "children": [{"kind": "page", "pageId": "home"}]}]}
        client.request("/_docs/api/v1/settings", "PUT", settings)
        require(b"Getting started" in client.request("/", parse=False), "Published navigation group is missing")
        require(b"Docker publication verification" in client.request("/", parse=False), "Reader did not update")
        exercise_source_and_folder_deletion(client)
        project_state = client.request("/_docs/api/v1/documents")
        created = client.request("/_docs/api/v1/documents", "POST", {
            "revision": project_state["revision"], "title": "Directory guide", "kind": "markdown",
            "path": "guide/index.md", "outputPath": "guide/index.html", "content": "# Nested directory guide\n"}, expected=201)
        nested_draft = created["draft"]
        client.request(f"/_docs/api/v1/drafts/{nested_draft['id']}/publish", "POST",
                       {"revision": nested_draft["revision"], "push": True, "message": "docs: add directory guide"})
        project_state = client.request("/_docs/api/v1/documents")
        client.request("/_docs/api/v1/branches", "POST", {"revision": project_state["revision"], "name": "review"})
        project_state = client.request("/_docs/api/v1/documents")
        client.request("/_docs/api/v1/branches/checkout", "POST", {"revision": project_state["revision"], "name": "review"})
        for action in ["push", "pull"]:
            project_state = client.request("/_docs/api/v1/documents")
            client.request("/_docs/api/v1/sync", "POST", {"revision": project_state["revision"], "action": action})
        project_state = client.request("/_docs/api/v1/documents")
        client.request("/_docs/api/v1/branches/checkout", "POST", {"revision": project_state["revision"], "name": "main"})
        document = client.request("/_docs/api/v1/documents/home")
        draft_text = "# Unpublished Docker draft\n"
        client.request("/_docs/api/v1/documents/home", "PUT", {"revision": document["revision"], "content": draft_text})
        print("Editor draft, publication, actual Git commit/push, checkout and pull passed", flush=True)

        viewer_password = secrets.token_urlsafe(32)
        client.request("/_docs/api/v1/users", "POST", {"username": "qa-reader", "displayName": "QA reader",
                       "password": viewer_password, "role": "viewer", "grants": {"read": ["index.md"], "write": []}}, expected=201)
        viewer = Client(origin)
        viewer.request("/_docs/api/v1/session", "POST", {"username": "qa-reader", "password": viewer_password})
        viewer_password = ""
        viewer.request("/", parse=False)
        document = viewer.request("/_docs/api/v1/documents/home")
        viewer.request("/_docs/api/v1/documents/home", "PUT",
                       {"revision": document["revision"], "content": "# Must remain forbidden"}, expected=403)
        viewer.request("/_docs/api/v1/users", expected=403)
        viewer.request("/guide/", expected=404, parse=False)
        viewer.request("/guide", expected=404, parse=False)
        print("Read-only role enforcement passed", flush=True)

        live_backup = compose("--profile", "maintenance", "run", "--rm", "--no-deps", "backup", expected=None)
        require(live_backup.returncode != 0, "Backup accepted a live CMS")
        compose("stop", "cms")
        compose("--profile", "maintenance", "run", "--rm", "--no-deps", "backup")
        compose("up", "-d", "--wait", "cms")
        assert_state(client, draft_text)
        compose("down")
        compose("up", "-d", "--wait", "cms")
        assert_state(client, draft_text)
        print("Offline backup guard and persistence across down/up passed", flush=True)
        compose("stop", "cms")
        refused = compose("--profile", "maintenance", "run", "--rm", "--no-deps", "restore", expected=None)
        require(refused.returncode != 0, "Restore accepted a non-empty data volume")

        restored = fresh_env("restored")
        compose("--profile", "maintenance", "run", "--rm", "--no-deps", "restore", environment=restored)
        compose("up", "-d", "--wait", "cms", environment=restored)
        assert_state(client, draft_text)
        restored_marker = shell("cat /data/ssh/backup-marker", environment=restored).stdout
        require(restored_marker == marker, "Private SSH state did not survive backup/restore")
        print("Fresh-volume restore preserved sessions, sources, drafts, Git and SSH state", flush=True)

        with tempfile.TemporaryDirectory(prefix="pb-cms-export-") as directory:
            compose("--profile", "maintenance", "create", "--no-build", "backup", environment=restored)
            container = compose("--profile", "maintenance", "ps", "--all", "-q", "backup", environment=restored).stdout.decode().strip()
            archive = Path(directory) / "qa-state.tar.gz"
            checksum = Path(directory) / "qa-state.tar.gz.sha256"
            run(["docker", "cp", f"{container}:/backup/qa-state.tar.gz", str(archive)])
            run(["docker", "cp", f"{container}:/backup/qa-state.tar.gz.sha256", str(checksum)])
            require(stat.S_IMODE(archive.stat().st_mode) == 0o600, "Exported archive permissions are not private")
            require(hashlib.sha256(archive.read_bytes()).hexdigest() == checksum.read_text().split()[0], "Exported archive checksum changed")
            compose("--profile", "maintenance", "rm", "-f", "backup", environment=restored)
            imported = fresh_env("imported-data", backup=project + "_imported-backups")
            volumes.add(imported["CMS_BACKUP_VOLUME"])
            for source, suffix in [(archive, ""), (checksum, ".sha256")]:
                compose("--profile", "maintenance", "run", "--rm", "--no-deps", "-T", "--entrypoint", "sh", "backup",
                        "-ec", f'umask 077; set -C; cat > "/backup/$CMS_BACKUP_FILE{suffix}"',
                        environment=imported, input_data=source.read_bytes())
            compose("stop", "cms", environment=restored)
            compose("--profile", "maintenance", "run", "--rm", "--no-deps", "restore", environment=imported)
            compose("up", "-d", "--wait", "cms", environment=imported)
            assert_state(client, draft_text)
            print("Private archive export/import and restore passed", flush=True)

        prefixed_env = imported.copy()
        prefixed_env["CMS_PUBLIC_URL"] = origin + "/team"
        compose("up", "-d", "--wait", "cms", environment=prefixed_env)
        prefixed = Client(origin, "/team")
        prefixed.cookies, prefixed.csrf = client.cookies.copy(), client.csrf
        require(prefixed.request("/_docs/health") == {"status": "ready"}, "Prefixed health check failed")
        prefixed.request("/_docs/admin/app.mjs", parse=False)
        assert_state(prefixed, draft_text)
        require(b"Nested directory guide" in prefixed.request("/guide/", parse=False), "Directory index alias did not resolve")
        bare = Client(origin)
        bare.cookies, bare.csrf = client.cookies.copy(), client.csrf
        bare.request("/team", expected=308, parse=False)
        require(bare.location == "/team/", "Bare hosting prefix did not redirect canonically")
        bare.request("/team/guide", expected=308, parse=False)
        require(bare.location == "/team/guide/", "Bare directory did not redirect canonically")
        restricted = Client(origin, "/team")
        restricted.cookies, restricted.csrf = viewer.cookies.copy(), viewer.csrf
        restricted.request("/guide/", expected=404, parse=False)
        restricted.request("/guide", expected=404, parse=False)
        restricted.request("/missing/", expected=404, parse=False)
        print("Hosting-prefix and directory aliases enforce canonical routes and read permissions", flush=True)

        for kind in ["checksum", "traversal", "symlink", "hardlink"]:
            payload = io.BytesIO()
            with tarfile.open(fileobj=payload, mode="w:gz") as archive:
                entry = tarfile.TarInfo("../../escape" if kind == "traversal" else "payload")
                if kind in {"symlink", "hardlink"}:
                    entry.type = tarfile.SYMTYPE if kind == "symlink" else tarfile.LNKTYPE
                    entry.linkname = "/tmp" if kind == "symlink" else "content/index.md"
                    archive.addfile(entry)
                else:
                    entry.size = 1
                    archive.addfile(entry, io.BytesIO(b"x"))
            bad = fresh_env("bad-" + kind, archive=kind + ".tar.gz")
            content = payload.getvalue()
            compose("--profile", "maintenance", "run", "--rm", "--no-deps", "-T", "--entrypoint", "sh", "backup",
                    "-ec", 'umask 077; cat > "/backup/$CMS_BACKUP_FILE"', environment=bad, input_data=content)
            digest = "0" * 64 if kind == "checksum" else hashlib.sha256(content).hexdigest()
            compose("--profile", "maintenance", "run", "--rm", "--no-deps", "-T", "--entrypoint", "sh", "backup",
                    "-ec", 'umask 077; cat > "/backup/$CMS_BACKUP_FILE.sha256"', environment=bad,
                    input_data=f"{digest}  {kind}.tar.gz\n".encode())
            refusal = compose("--profile", "maintenance", "run", "--rm", "--no-deps", "restore", environment=bad, expected=None)
            require(refusal.returncode != 0, "Restore accepted an unsafe archive")
            compose("--profile", "maintenance", "run", "--rm", "--no-deps", "--entrypoint", "sh", "backup",
                    "-ec", '[ -z "$(find /data -mindepth 1 -print -quit)" ]', environment=bad)
        print("Bad checksums, traversal, symbolic links and hard links rejected before data writes", flush=True)
        if not options.skip_static:
            with socket.socket() as listener:
                listener.bind(("127.0.0.1", 0))
                static_port = listener.getsockname()[1]
            static_env = base_env.copy()
            static_env["STATIC_DOCS_PORT"] = str(static_port)
            compose("--profile", "static", "build", "docs", environment=static_env, timeout=300)
            compose("--profile", "static", "up", "-d", "--wait", "docs", environment=static_env)
            static = Client(f"http://localhost:{static_port}")
            require(static.request("/healthz", parse=False).strip() == b"ok", "Static image health failed")
            require(b"<html" in static.request("/", parse=False), "Static reader did not serve HTML")
            project_model = static.request("/_docs/project.json")
            require(project_model.get("pages"), "Static project model did not contain documentation")
            print("Separate static image and reader passed", flush=True)
        container = compose("ps", "-q", "cms").stdout.decode().strip()
        metrics = run(["docker", "stats", "--no-stream", "--format", "{{json .}}", container]).stdout
        if metrics.strip():
            selected = json.loads(metrics)
            print("Observed CMS resource sample: " + selected["CPUPerc"] + " CPU, " + selected["MemUsage"], flush=True)
        print("Docker CMS integration checks passed", flush=True)
    finally:
        cleanup = compose("--profile", "static", "--profile", "maintenance", "down", "--remove-orphans", environment=base_env, expected=None)
        if cleanup.returncode != 0:
            print("QA cleanup needs attention: Compose down failed", flush=True)
        for volume in sorted(volumes):
            run(["docker", "volume", "rm", volume], expected=None)


if __name__ == "__main__":
    main()
