#!/usr/bin/env python3
"""Local-only CMS preview with isolated state and conservative live updates."""

import argparse
import fcntl
import getpass
import http.client
import json
import os
import re
from pathlib import Path
import signal
import socket
import subprocess
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parents[2]
ASSETS = ROOT / "internal/studio/web/assets"
HOP_HEADERS = {"connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
               "te", "trailer", "transfer-encoding", "upgrade", "content-length"}
LIVE_SCRIPT = b"""(() => {
  let previous;
  const admin = location.pathname.startsWith('/_docs/admin/');
  const notice = () => {
    if (document.getElementById('pb-dev-update')) return;
    const box = document.createElement('div');
    box.id = 'pb-dev-update';
    box.style.cssText = 'position:fixed;right:16px;bottom:16px;z-index:2147483647;padding:12px 16px;background:#142331;color:white;border:1px solid #8ca5b5;border-radius:8px;font:14px system-ui;box-shadow:0 4px 20px #0004';
    box.append('Development update ready. Save your work, then ');
    const button = document.createElement('button');
    button.textContent = 'Reload';
    button.onclick = () => location.reload();
    box.append(button);
    document.body.append(box);
  };
  async function poll() {
    try {
      const next = await (await fetch('/__dev/version', {cache:'no-store'})).json();
      if (previous && next.css !== previous.css) {
        document.querySelectorAll('link[rel="stylesheet"]').forEach(link => {
          const url = new URL(link.href); url.searchParams.set('__dev', next.css); link.href = url;
        });
      }
      if (previous && next.code !== previous.code) {
        if (admin) notice(); else location.reload();
      }
      previous = next;
    } catch (_) { /* Keep the editor open during rebuilds. */ }
    setTimeout(poll, 750);
  }
  poll();
})();
"""


def default_state():
    return Path("/dev-data")


def child_environment():
    # Never inherit production CMS configuration or pass the bootstrap password on.
    env = {key: value for key, value in os.environ.items()
           if not key.startswith("CMS_") and key != "DEV_ADMIN_PASSWORD"}
    env.setdefault("GOTOOLCHAIN", "go1.26.10")
    env["CGO_ENABLED"] = "0"
    return env


def snapshot():
    paths = set()
    ignored = {".git", "node_modules", "build", "site", ".task", "__pycache__"}
    for directory, children, files in os.walk(ROOT):
        children[:] = [name for name in children if name not in ignored]
        reader = Path(directory) == ROOT / "internal/site"
        paths.update(Path(directory) / name for name in files
                     if name.endswith(".go") or (reader and name.endswith(".js")))
    paths.update(ROOT / name for name in ("go.mod", "go.sum"))
    web = ROOT / "internal/studio/web"
    for directory in (web / "assets", web / "src"):
        for parent, children, files in os.walk(directory):
            children[:] = [name for name in children if name not in ignored]
            paths.update(Path(parent) / name for name in files)
    paths.update(web / name for name in ("build.mjs", "package.json", "package-lock.json"))
    stamps = {}
    for path in paths:
        try:
            stat = path.stat()
            stamps[path] = (stat.st_mtime_ns, stat.st_size)
        except OSError:
            pass  # Editors can rename or remove a file between scanning and stat.
    return stamps


def changed(before, after):
    return {path for path in before.keys() | after.keys() if before.get(path) != after.get(path)}


def classify(paths):
    css = any(path.parent == ASSETS and path.suffix == ".css" for path in paths)
    web = ROOT / "internal/studio/web"
    editor = any(web / "src" in path.parents or path.name in
                 {"build.mjs", "package.json", "package-lock.json"} for path in paths)
    code = any(not (path.parent == ASSETS and path.suffix == ".css") for path in paths)
    # Administration modules are served directly from disk. Embedded Go resources
    # and reader code need a rebuild before announcing their update.
    go = any(ASSETS not in path.parents and web / "src" not in path.parents
             and path.name not in {"build.mjs", "package.json", "package-lock.json"}
             for path in paths)
    return css, code, editor, go


class Preview:
    def __init__(self, state, port, bind):
        self.state, self.port, self.bind = state, port, bind
        self.data = state / "cms"
        self.binary = state / "pb-md5-generator"
        self.env = child_environment()
        self.stop = threading.Event()
        self.versions = {"css": 0, "code": 0}
        self.process = None
        self.server = None
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            self.backend_port = listener.getsockname()[1]

    @property
    def url(self):
        return f"http://localhost:{self.port}"

    def build(self):
        candidate = self.state / "pb-md5-generator.next"
        print("Building Go application…", flush=True)
        result = subprocess.run(["go", "build", "-o", str(candidate),
                                 "./cmd/pb-md5-generator"], cwd=ROOT, env=self.env)
        if result.returncode:
            print("Build failed; the last working application is still running.", flush=True)
            return False
        candidate.replace(self.binary)
        return True

    def build_editor(self, install=False):
        web = ROOT / "internal/studio/web"
        if install or not (web / "node_modules/esbuild").is_dir():
            if subprocess.run(["npm", "ci", "--prefix", str(web)], cwd=ROOT,
                              env=self.env).returncode:
                return False
        return subprocess.run(["npm", "--prefix", str(web), "run", "build"],
                              cwd=ROOT, env=self.env).returncode == 0

    def bootstrap(self):
        if (self.data / "state/auth/auth.json").exists():
            return
        password = os.environ.pop("DEV_ADMIN_PASSWORD", None)
        if password is None:
            if not sys.stdin.isatty():
                raise RuntimeError("First start needs a terminal for the hidden password prompt, or DEV_ADMIN_PASSWORD.")
            password = getpass.getpass("Password for development administrator 'owner' (at least 10 characters): ")
            if password != getpass.getpass("Confirm password: "):
                raise RuntimeError("Passwords do not match.")
        if len(password) < 10:
            raise RuntimeError("Development administrator password must have at least 10 characters.")
        try:
            result = subprocess.run([str(self.binary), "admin-user", "--data-dir", str(self.data),
                                     "--public-url", self.url, "--username", "owner",
                                     "--display-name", "Owner", "--password-stdin"],
                                    input=password.encode(), cwd=self.state, env=self.env)
            if result.returncode:
                raise RuntimeError("Could not create the development administrator.")
        finally:
            password = None

    def stop_backend(self):
        if self.process is not None:
            self.process.terminate()
            try:
                self.process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait()
            self.process = None

    def start_backend(self):
        self.stop_backend()
        self.process = subprocess.Popen([str(self.binary), "serve", "--data-dir", str(self.data),
                                         "--public-url", self.url, "--listen",
                                         f"127.0.0.1:{self.backend_port}", "--storage", "local", "--init"],
                                        cwd=self.state, env=self.env)
        deadline = time.monotonic() + 30
        while not self.stop.is_set() and time.monotonic() < deadline:
            if self.process.poll() is not None:
                raise RuntimeError("Development CMS exited during startup.")
            try:
                with socket.create_connection(("127.0.0.1", self.backend_port), timeout=.2):
                    return
            except OSError:
                self.stop.wait(.1)
        if not self.stop.is_set():
            raise RuntimeError("Development CMS did not become ready.")

    def handler(self):
        preview = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass  # Request paths and parameters can contain private editor data.

            def respond(self, status, body, content_type):
                self.send_response(status)
                self.send_header("Content-Type", content_type)
                self.send_header("Content-Length", str(len(body)))
                self.send_header("Cache-Control", "no-store")
                self.end_headers()
                if self.command != "HEAD":
                    self.wfile.write(body)

            def proxy(self):
                if self.headers.get("Host") != f"localhost:{preview.port}":
                    self.respond(421, b"Use the printed localhost URL.", "text/plain")
                    return
                path = urlsplit(self.path).path
                if path == "/__dev/version":
                    self.respond(200, json.dumps(preview.versions).encode(), "application/json")
                    return
                if path == "/__dev/live.js":
                    self.respond(200, LIVE_SCRIPT, "text/javascript")
                    return
                name = path.removeprefix("/_docs/admin/")
                if path.startswith("/_docs/admin/") and "/" not in name:
                    asset = ASSETS / name
                    if asset.suffix in {".css", ".mjs", ".js"} and asset.is_file():
                        kind = "text/css" if asset.suffix == ".css" else "text/javascript"
                        self.respond(200, asset.read_bytes(), kind)
                        return
                if self.headers.get("Transfer-Encoding"):
                    self.respond(400, b"Transfer-Encoding is not supported by the development proxy.", "text/plain")
                    return
                connection = http.client.HTTPConnection("127.0.0.1", preview.backend_port, timeout=30)
                try:
                    length = int(self.headers.get("Content-Length", "0"))
                    if length < 0 or length > 64 * 1024 * 1024:
                        self.respond(413, b"Request too large.", "text/plain")
                        return
                    body = self.rfile.read(length) if length else None
                    headers = {key: value for key, value in self.headers.items()
                               if key.lower() not in HOP_HEADERS}
                    headers["Accept-Encoding"] = "identity"
                    connection.request(self.command, self.path, body=body, headers=headers)
                    response = connection.getresponse()
                    html = "text/html" in response.getheader("Content-Type", "")
                    content = None
                    if html and self.command != "HEAD":
                        content = response.read(16 * 1024 * 1024 + 1)
                        if len(content) > 16 * 1024 * 1024:
                            self.respond(502, b"Development HTML response is too large.", "text/plain")
                            return
                        policy = response.getheader("Content-Security-Policy", "")
                        nonce = re.search(r"'nonce-([A-Za-z0-9+/_=-]+)'", policy)
                        attribute = (' nonce="' + nonce.group(1) + '"') if nonce else ""
                        script = ('<script src="/__dev/live.js"' + attribute + '></script></body>').encode()
                        content = content.replace(b"</body>", script)
                    self.send_response(response.status)
                    for key, value in response.getheaders():
                        if key.lower() not in HOP_HEADERS and key.lower() != "cache-control":
                            self.send_header(key, value)
                    if content is not None:
                        self.send_header("Content-Length", str(len(content)))
                    elif response.getheader("Content-Length") is not None:
                        self.send_header("Content-Length", response.getheader("Content-Length"))
                    self.send_header("Cache-Control", "no-store")
                    self.end_headers()
                    if self.command != "HEAD":
                        if content is not None:
                            self.wfile.write(content)
                        else:
                            while chunk := response.read(64 * 1024):
                                self.wfile.write(chunk)
                except (BrokenPipeError, ConnectionResetError):
                    pass
                except (OSError, http.client.HTTPException, ValueError):
                    self.respond(503, b"Development CMS is restarting. Retry shortly.", "text/plain")
                finally:
                    connection.close()

            do_GET = do_HEAD = do_POST = do_PUT = do_PATCH = do_DELETE = do_OPTIONS = proxy

        return Handler

    def run(self):
        self.server = ThreadingHTTPServer((self.bind, self.port), self.handler())
        self.server.daemon_threads = True
        try:
            if not self.build_editor() or not self.build():
                raise RuntimeError("Initial development build failed.")
            if not (self.data / "state/auth/auth.json").exists():
                raise RuntimeError("The development owner is missing. Use task dev to bootstrap it first.")
            self.start_backend()
            threading.Thread(target=self.server.serve_forever, daemon=True).start()
            print(f"\nDocumentation: {self.url}/\nAdmin: {self.url}/_docs/admin/\n"
                  f"Development data: {self.data}\nAdministrator: owner\n"
                  "Watching source files. CSS updates live; admin code updates offer a reload. Ctrl+C stops.\n", flush=True)
            previous = snapshot()
            pending = set()
            deadline = 0
            while not self.stop.wait(.2):
                current = snapshot()
                updates = changed(previous, current)
                previous = current
                if updates:
                    pending.update(updates)
                    deadline = time.monotonic() + .35
                if not pending or time.monotonic() < deadline:
                    continue
                css, code, editor, go = classify(pending)
                install = any(path.name in {"package.json", "package-lock.json"} for path in pending)
                pending.clear()
                if editor and not self.build_editor(install):
                    print("Editor build failed; correct the error and save again.", flush=True)
                    continue
                if go:
                    if not self.build():
                        continue
                    self.start_backend()
                if css:
                    self.versions["css"] += 1
                if code:
                    self.versions["code"] += 1
                print("Development preview updated.", flush=True)
                # Keep edits made during a build visible on the next scan. Only
                # acknowledge generated editor output that this build produced.
                if editor:
                    for path, stamp in snapshot().items():
                        if path.parent == ASSETS and path.name in {"editor.js", "THIRD_PARTY_NOTICES.txt"}:
                            previous[path] = stamp
        finally:
            self.stop_backend()
            self.server.server_close()


def main():
    if os.environ.get("PB_DOCS_DEV_CONTAINER") != "1":
        print("Use task dev: development processes and data run inside Docker.", file=sys.stderr)
        return 1
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--port", type=int, default=8091)
    parser.add_argument("--bootstrap-only", action="store_true")
    parser.add_argument("--bind", default="127.0.0.1", choices=["127.0.0.1", "0.0.0.0"])
    parser.add_argument("--state-dir", type=Path, default=default_state())
    args = parser.parse_args()
    if not 1024 <= args.port <= 65535:
        parser.error("--port must be between 1024 and 65535")
    state = args.state_dir.expanduser().resolve()
    if state == ROOT or ROOT in state.parents:
        parser.error("--state-dir must be outside the development checkout")
    state.mkdir(parents=True, exist_ok=True, mode=0o700)
    with (state / ".dev.lock").open("w") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            parser.error("another development preview is using this state directory")
        preview = Preview(state, args.port, args.bind)
        def stop_preview(*_args):
            preview.stop.set()
            raise KeyboardInterrupt

        for signum in (signal.SIGINT, signal.SIGTERM):
            signal.signal(signum, stop_preview)
        try:
            if args.bootstrap_only:
                if (preview.data / "state/auth/auth.json").exists():
                    print("Keeping the existing development owner and data.", flush=True)
                    return 0
                if not preview.build_editor() or not preview.build():
                    raise RuntimeError("Initial development build failed.")
                preview.bootstrap()
            else:
                preview.run()
        except KeyboardInterrupt:
            return 130
        except (RuntimeError, OSError) as error:
            print(f"Development preview: {error}", file=sys.stderr)
            return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
