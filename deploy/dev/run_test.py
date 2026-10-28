"""Focused watcher and HTTP regression checks; run inside the dev image."""

import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import subprocess
import tempfile
import threading
import unittest
from unittest.mock import patch

import run as dev


class WatcherTests(unittest.TestCase):
    def test_source_classification_keeps_css_and_modules_in_place(self):
        self.assertEqual(dev.classify({dev.ASSETS / "studio.css"}), (True, False, False, False))
        self.assertEqual(dev.classify({dev.ASSETS / "app.mjs"}), (False, True, False, False))
        self.assertEqual(dev.classify({dev.ROOT / "internal/site/ui.go"}), (False, True, False, True))
        self.assertEqual(dev.classify({dev.ROOT / "internal/studio/web/src/editor.mjs"}),
                         (False, True, True, False))

    def test_watcher_prunes_dependencies_and_handles_disappearing_files(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name in ("main.go", "internal/site/ui.go", "internal/site/api-client.js",
                         "node_modules/skip.go", ".git/skip.go", "build/skip.go", "site/skip.go", "tmp/skip.go"):
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("package main\n")
            with patch.object(dev, "ROOT", root):
                files = dev.snapshot()
                self.assertIn(root / "main.go", files)
                self.assertIn(root / "internal/site/ui.go", files)
                self.assertIn(root / "internal/site/api-client.js", files)
                self.assertNotIn(root / "site/skip.go", files)
                self.assertNotIn(root / "tmp/skip.go", files)
                self.assertNotIn(root / "node_modules/skip.go", files)
                self.assertNotIn(root / ".git/skip.go", files)
                self.assertNotIn(root / "build/skip.go", files)
                with patch.object(Path, "stat", side_effect=FileNotFoundError):
                    self.assertEqual(dev.snapshot(), {})

    def test_embedded_font_changes_rebuild_the_reader(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            font = root / "internal/uiassets/fonts/dm-sans-latin.woff2"
            font.parent.mkdir(parents=True)
            font.write_bytes(b"original font")
            with patch.object(dev, "ROOT", root):
                before = dev.snapshot()
                font.write_bytes(b"updated font bytes")
                paths = dev.changed(before, dev.snapshot())
                self.assertEqual(paths, {font})
                self.assertEqual(dev.classify(paths), (False, True, False, True))

    def test_failed_build_preserves_the_previous_binary(self):
        with tempfile.TemporaryDirectory() as directory:
            preview = dev.Preview(Path(directory), 8091, "127.0.0.1")
            preview.binary.write_bytes(b"last working binary")
            with patch.object(dev.subprocess, "run", return_value=subprocess.CompletedProcess([], 1)):
                self.assertFalse(preview.build())
            self.assertEqual(preview.binary.read_bytes(), b"last working binary")

    def test_interrupt_cancels_bootstrap_before_a_password_is_requested(self):
        handlers = {}

        def register(signum, handler):
            handlers[signum] = handler

        def interrupt():
            handlers[dev.signal.SIGINT]()

        with tempfile.TemporaryDirectory() as directory:
            with patch.dict(dev.os.environ, {"PB_DOCS_DEV_CONTAINER": "1"}), \
                    patch.object(dev.sys, "argv", ["run.py", "--bootstrap-only", "--state-dir", directory]), \
                    patch.object(dev.signal, "signal", side_effect=register), \
                    patch.object(dev.Preview, "build_editor", side_effect=interrupt):
                self.assertEqual(dev.main(), 130)

    def test_child_environment_does_not_forward_production_or_bootstrap_secrets(self):
        with patch.dict(dev.os.environ, {"CMS_DATA_DIR": "/production", "DEV_ADMIN_PASSWORD": "secret"}):
            environment = dev.child_environment()
        self.assertFalse(any(name.startswith("CMS_") for name in environment))
        self.assertNotIn("DEV_ADMIN_PASSWORD", environment)


class ProxyTests(unittest.TestCase):
    def setUp(self):
        class Backend(BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass

            def do_GET(self):
                payload = b"x" * (256 * 1024) if self.path == "/binary" else b"<html><body>Reader</body></html>"
                self.send_response(200)
                self.send_header("Content-Type", "application/octet-stream" if self.path == "/binary" else "text/html")
                self.send_header("Content-Length", str(len(payload)))
                self.send_header("Content-Security-Policy", "script-src 'nonce-test123'")
                self.end_headers()
                self.wfile.write(payload)

            def do_HEAD(self):
                self.send_response(200)
                self.send_header("Content-Length", "12345")
                self.end_headers()

        self.backend = ThreadingHTTPServer(("127.0.0.1", 0), Backend)
        threading.Thread(target=self.backend.serve_forever, daemon=True).start()
        self.directory = tempfile.TemporaryDirectory()
        self.preview = dev.Preview(Path(self.directory.name), 8091, "127.0.0.1")
        self.preview.backend_port = self.backend.server_port
        self.proxy = ThreadingHTTPServer(("127.0.0.1", 0), self.preview.handler())
        self.preview.port = self.proxy.server_port
        threading.Thread(target=self.proxy.serve_forever, daemon=True).start()

    def tearDown(self):
        for server in (self.proxy, self.backend):
            server.shutdown()
            server.server_close()
        self.directory.cleanup()

    def request(self, path="/", method="GET", headers=None):
        connection = http.client.HTTPConnection("127.0.0.1", self.proxy.server_port)
        request_headers = {"Host": f"localhost:{self.proxy.server_port}"}
        request_headers.update(headers or {})
        connection.request(method, path, headers=request_headers)
        response = connection.getresponse()
        status, fields, body = response.status, dict(response.getheaders()), response.read()
        connection.close()
        return status, fields, body

    def test_csp_nonce_is_preserved_on_the_development_script(self):
        status, headers, body = self.request()
        self.assertEqual(status, 200)
        self.assertIn(b'<script src="/__dev/live.js" nonce="test123">', body)
        self.assertEqual(headers["Content-Security-Policy"], "script-src 'nonce-test123'")
        self.assertEqual(int(headers["Content-Length"]), len(body))

    def test_head_preserves_backend_length(self):
        status, headers, body = self.request(method="HEAD")
        self.assertEqual((status, headers["Content-Length"], body), (200, "12345", b""))

    def test_streamed_binary_is_not_changed(self):
        status, headers, body = self.request("/binary")
        self.assertEqual(status, 200)
        self.assertEqual(len(body), 256 * 1024)
        self.assertEqual(int(headers["Content-Length"]), len(body))
        self.assertEqual(body, b"x" * len(body))

    def test_foreign_hosts_and_unhandled_transfer_encoding_are_rejected(self):
        self.assertEqual(self.request(headers={"Host": "foreign.invalid"})[0], 421)
        self.assertEqual(self.request(headers={"Transfer-Encoding": "chunked"})[0], 400)


if __name__ == "__main__":
    unittest.main()
