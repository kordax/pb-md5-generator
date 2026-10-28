#!/usr/bin/env python3
"""Bounded disposable CMS resource acceptance; --self-test never starts Docker."""

import argparse
from collections import Counter
import importlib.util
import io
import json
import math
import os
from pathlib import Path, PurePosixPath
import secrets
import signal
import socket
import stat
import statistics
import subprocess
import sys
import tarfile
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
IMAGE = "pb-docs-cms:local"
LABEL = "org.pb-docs.resource-acceptance"
PREFIX = "pb-cms-resource-"
MEMORY = 512 << 20
PLATEAU = 32 << 20
LIMITS = ["--memory=512m", "--memory-swap=512m", "--cpus=1", "--read-only",
          "--cap-drop=ALL", "--security-opt=no-new-privileges", "--no-healthcheck",
          "--user=65532:65532", "--tmpfs=/tmp:rw,noexec,nosuid,nodev,size=32m,mode=1777"]
CAVEAT = ("Cold process startup; host filesystem cache not cleared; cgroup page-cache charges "
          "can predate the measured container. RSS is reported separately from cgroup file cache. "
          "Six finite repeated batches provide a bounded plateau check, not proof of no leaks.")


class AcceptanceError(RuntimeError):
    pass


def require(condition, code):
    if not condition:
        raise AcceptanceError(code)


def load_module(name, filename):
    spec = importlib.util.spec_from_file_location(name, ROOT / "testdata" / filename)
    require(spec is not None and spec.loader is not None, "module_unavailable")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def command(arguments, *, data=None, timeout=30, check=True):
    result = subprocess.run(arguments, input=data, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                            cwd=ROOT, timeout=timeout)
    require(not check or result.returncode == 0, "command_failed")
    return result


def docker(*arguments, **options):
    return command(["docker", *arguments], **options)


def local_docker():
    require(sys.platform.startswith("linux"), "linux_required")
    context = os.environ.get("DOCKER_CONTEXT")
    endpoint = os.environ.get("DOCKER_HOST") if not context else None
    if not endpoint:
        if not context:
            context = docker("context", "show").stdout.decode().strip()
        info = json.loads(docker("context", "inspect", context).stdout)
        endpoint = info[0]["Endpoints"]["docker"]["Host"]
    parsed = urllib.parse.urlsplit(endpoint)
    require(parsed.scheme == "unix" and not parsed.netloc and parsed.path.startswith("/"), "local_unix_docker_required")
    require(stat.S_ISSOCK(os.stat(parsed.path).st_mode), "local_docker_socket_required")
    info = json.loads(docker("info", "--format", "{{json .}}").stdout)
    require(info.get("OSType") == "linux" and str(info.get("CgroupVersion")) == "2", "cgroup_v2_required")
    docker("image", "inspect", IMAGE)
    return {"docker_server_version": info.get("ServerVersion", "unknown"), "kernel_release": os.uname().release}


class OwnedDocker:
    def __init__(self):
        self.token = secrets.token_hex(12)
        self.prefix = PREFIX + str(os.getpid()) + "-" + self.token
        self.volume = self.prefix + "-data"
        self.containers = []
        self.volume_attempted = False

    def inspect(self, name):
        result = docker("container", "inspect", name, check=False)
        return json.loads(result.stdout)[0] if result.returncode == 0 else None

    def create_volume(self):
        require(docker("volume", "inspect", self.volume, check=False).returncode != 0, "volume_collision")
        self.volume_attempted = True
        docker("volume", "create", "--label", LABEL + "=" + self.token, self.volume)
        info = json.loads(docker("volume", "inspect", self.volume).stdout)[0]
        require(info.get("Labels", {}).get(LABEL) == self.token, "volume_ownership_mismatch")

    def arguments(self, suffix):
        name = self.prefix + "-" + suffix
        require(name.startswith(PREFIX) and self.inspect(name) is None, "container_collision")
        self.containers.append(name)
        return name, ["--name", name, "--label", LABEL + "=" + self.token, *LIMITS,
                      "--mount", "type=volume,src=" + self.volume + ",dst=/data"]

    def helper(self, suffix, entrypoint, arguments, data=None):
        _, flags = self.arguments(suffix)
        docker("run", "--rm", "--pull=never", "--network=none", "-i", *flags,
               "--entrypoint", entrypoint, IMAGE, *arguments, data=data, timeout=120)

    def server(self, suffix, port, public_url, initialize):
        name, flags = self.arguments(suffix)
        docker("create", "--pull=never", *flags, "--publish", f"127.0.0.1:{port}:8090",
               "--entrypoint", "pb-md5-generator", IMAGE, "serve", "--data-dir", "/data",
               "--listen", "0.0.0.0:8090", "--public-url", public_url, "--storage", "local",
               "--init=" + ("true" if initialize else "false"))
        return name

    def running(self, name, pid=None):
        info = self.inspect(name)
        require(info is not None and info.get("Config", {}).get("Labels", {}).get(LABEL) == self.token, "container_ownership_mismatch")
        state = info["State"]
        require(state.get("Running") and not state.get("OOMKilled") and info.get("RestartCount") == 0, "container_stopped_oom_or_restarted")
        require(pid is None or state["Pid"] == pid, "container_pid_changed")
        host = info["HostConfig"]
        require(host["Memory"] == MEMORY and host["MemorySwap"] == MEMORY and host["NanoCpus"] == 1000000000,
                "container_limits_mismatch")
        require(host["ReadonlyRootfs"] and "ALL" in host["CapDrop"] and any("no-new-privileges" in value for value in host["SecurityOpt"]),
                "container_security_mismatch")
        return info

    def cleanup(self):
        failed = False
        for name in reversed(self.containers):
            try:
                info = self.inspect(name)
                if info is None:
                    continue
                require(name.startswith(self.prefix + "-") and info.get("Config", {}).get("Labels", {}).get(LABEL) == self.token,
                        "cleanup_container_ownership")
                failed |= docker("rm", "--force", name, check=False, timeout=30).returncode != 0
            except Exception:
                failed = True
        if self.volume_attempted:
            try:
                result = docker("volume", "inspect", self.volume, check=False)
                if result.returncode == 0:
                    info = json.loads(result.stdout)[0]
                    require(self.volume.startswith(self.prefix) and info.get("Labels", {}).get(LABEL) == self.token,
                            "cleanup_volume_ownership")
                    failed |= docker("volume", "rm", self.volume, check=False, timeout=30).returncode != 0
            except Exception:
                failed = True
        return not failed


def numeric_lines(text):
    result = {}
    for line in text.splitlines():
        fields = line.split()
        if len(fields) == 2 and fields[1].isdigit():
            result[fields[0]] = int(fields[1])
    return result


def proc_memory(text):
    result = {}
    for line in text.splitlines():
        key, _, value = line.partition(":")
        if key in {"VmRSS", "VmHWM", "RssAnon", "RssFile", "RssShmem"}:
            fields = value.split()
            require(len(fields) == 2 and fields[0].isdigit() and fields[1] == "kB", "proc_memory_format")
            result[key] = int(fields[0]) * 1024
    require("VmRSS" in result and "VmHWM" in result, "proc_memory_missing")
    return result


class Sampler:
    def __init__(self, info):
        self.pid, self.identity = int(info["State"]["Pid"]), info["Id"]
        require(self.pid > 0 and len(self.identity) == 64, "invalid_container_identity")
        self.proc = Path("/proc") / str(self.pid)
        self.relative = self.group_path()
        self.group = (Path("/sys/fs/cgroup") / self.relative.lstrip("/")).resolve()
        require(Path("/sys/fs/cgroup") in self.group.parents, "invalid_cgroup_location")
        require((self.group / "memory.max").read_text().strip() == str(MEMORY), "cgroup_memory_limit_mismatch")
        require((self.group / "memory.swap.max").read_text().strip() == "0", "cgroup_swap_not_disabled")
        self.started, self.samples, self.error = time.monotonic(), [], None
        self.lock, self.stop_event = threading.Lock(), threading.Event()
        self.thread = threading.Thread(target=self.loop, daemon=True)

    def group_path(self):
        rows = (self.proc / "cgroup").read_text().splitlines()
        paths = [row[3:] for row in rows if row.startswith("0::")]
        require(len(paths) == 1 and paths[0].startswith("/") and self.identity in paths[0], "host_pid_cgroup_identity_mismatch")
        return paths[0]

    def take(self):
        require(self.group_path() == self.relative, "host_pid_cgroup_changed")
        rss = proc_memory((self.proc / "status").read_text())
        events = numeric_lines((self.group / "memory.events").read_text())
        cpu = numeric_lines((self.group / "cpu.stat").read_text())
        memory = numeric_lines((self.group / "memory.stat").read_text())
        require("usage_usec" in cpu and "file" in memory and "anon" in memory, "cgroup_metrics_missing")
        sample = {"t_s": round(time.monotonic() - self.started, 6), "rss_bytes": rss["VmRSS"], "hwm_bytes": rss["VmHWM"],
                  "rss_anon_bytes": rss.get("RssAnon", 0), "rss_file_bytes": rss.get("RssFile", 0),
                  "memory_current_bytes": int((self.group / "memory.current").read_text()),
                  "memory_peak_bytes": int((self.group / "memory.peak").read_text()),
                  "memory_stat": memory, "memory_events": events, "cpu_stat": cpu}
        require(self.group_path() == self.relative, "host_pid_cgroup_changed")
        with self.lock:
            require(len(self.samples) < 4000, "sampler_limit")
            self.samples.append(sample)
        require(all(events.get(key, 0) == 0 for key in ("oom", "oom_kill", "oom_group_kill")), "cgroup_oom_event")
        return sample

    def loop(self):
        while not self.stop_event.is_set():
            try:
                self.take()
            except Exception as error:
                self.error = str(error) if isinstance(error, AcceptanceError) else type(error).__name__
                return
            self.stop_event.wait(0.5)

    def close(self):
        self.stop_event.set()
        if self.thread.ident is not None:
            self.thread.join(timeout=2)


def fixture_tar(root):
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode="w") as archive:
        for path in sorted(root.rglob("*")):
            relative = path.relative_to(root)
            require(not path.is_symlink() and all(not part.startswith(".") for part in relative.parts), "unsafe_fixture_entry")
            require(path.is_file() or path.is_dir(), "nonregular_fixture_entry")
            info = archive.gettarinfo(str(path), str(relative))
            info.uid = info.gid = 65532
            info.uname = info.gname = ""
            info.mode = 0o700 if path.is_dir() else 0o600
            if path.is_file():
                with path.open("rb") as source:
                    archive.addfile(info, source)
            else:
                archive.addfile(info)
    return output.getvalue()


def safe_path(value):
    path = PurePosixPath(value)
    require(isinstance(value, str) and value and not path.is_absolute() and str(path) == value
            and all(part not in {".", ".."} and not part.startswith(".") for part in path.parts)
            and not any(char in value for char in "\\\r\n\x00?#"), "unsafe_fixture_path")
    return "/" + urllib.parse.quote(value, safe="/")


def plans(metadata):
    hot = metadata["hot_pages"]
    cold = [page for page in metadata["pages"] if page["public"] and page not in hot]
    assets, private = metadata["assets"], metadata["private_page"]
    hot_plan = [(role, safe_path(page[key]), 200) for role in ("owner", "viewer")
                for page in hot for key in ("path", "data_path")]
    hot_plan += [(role, safe_path(asset["path"]), 200) for role in ("owner", "viewer") for asset in assets[:4]]
    require(len(set(hot_plan)) <= 64, "hot_working_set_too_large")
    batch = []
    for group in range(32):
        warm, cool, asset = hot[group % len(hot)], cold[(group * 13) % len(cold)], assets[group % len(assets)]
        batch += [("owner", safe_path(warm["path"]), 200), ("viewer", safe_path(warm["path"]), 200),
                  ("owner", safe_path(warm["data_path"]), 200), ("viewer", safe_path(warm["data_path"]), 200),
                  ("owner", safe_path(cool["path"]), 200), ("viewer", safe_path(cool["data_path"]), 200),
                  ("owner", safe_path(asset["path"]), 200), ("viewer", safe_path(asset["path"]), 200)]
    batch[-4:] = [("owner", safe_path(private["path"]), 200), ("viewer", safe_path(private["path"]), 404),
                  ("owner", safe_path(private["data_path"]), 200), ("viewer", safe_path(private["data_path"]), 404)]
    require(len(batch) == 256, "batch_size_mismatch")
    return hot_plan, batch


def wait_ready(origin, deadline):
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    while time.monotonic() < deadline:
        try:
            with opener.open(origin + "/bench/_docs/api/v1/session", timeout=1) as response:
                if response.status == 200:
                    return
        except urllib.error.HTTPError as error:
            if error.code == 401:
                return
        except (urllib.error.URLError, TimeoutError, ConnectionError):
            pass
        time.sleep(0.25)
    raise AcceptanceError("readiness_deadline")


def summary(samples, elapsed, latencies, cpu_before):
    require(samples, "phase_has_no_samples")
    latest = samples[-1]
    ordered = sorted(latencies)
    return {"elapsed_s": round(elapsed, 6), "requests": len(ordered),
            "latency_ms": {"p50": statistics.median(ordered), "p95": ordered[max(0, math.ceil(len(ordered) * 0.95) - 1)], "max": max(ordered)} if ordered else {},
            "cpu_seconds": round((latest["cpu_stat"]["usage_usec"] - cpu_before) / 1000000, 6),
            "rss_peak_bytes": max(row["rss_bytes"] for row in samples),
            "rss_tail_median_bytes": int(statistics.median([row["rss_bytes"] for row in samples[-4:]])),
            "hwm_bytes": max(row["hwm_bytes"] for row in samples),
            "cgroup_peak_bytes": max(row["memory_peak_bytes"] for row in samples),
            "cgroup_file_peak_bytes": max(row["memory_stat"]["file"] for row in samples),
            "raw_samples": samples}


def request_label(path):
    clean = path.split("?", 1)[0]
    if clean.startswith("/_docs/api/v1/"):
        parts = clean.split("/")
        if len(parts) > 5:
            parts[5] = "{id}"
        return "admin", "/".join(parts)
    if clean == "/_docs/search.json":
        return "search", clean
    if clean == "/_docs/project.json":
        return "project", clean
    if clean.startswith("/assets/"):
        return "asset", clean
    return ("page_json" if clean.startswith("/_docs/pages/") else "html"), clean


def timing_summary(records):
    return {"completed_requests": sum(row["ok"] for row in records),
            "failed_requests": sum(not row["ok"] for row in records),
            "request_elapsed_s": round(sum(row["elapsed_ms"] for row in records) / 1000, 6),
            "request_categories": dict(Counter(row["role"] + ":" + row["category"] for row in records)),
            "slowest_requests": sorted(records, key=lambda row: row["elapsed_ms"], reverse=True)[:10]}


def write_report(path, report):
    path = Path(path).absolute()
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    require(not path.is_symlink(), "report_symlink")
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, "w") as target:
        os.fchmod(target.fileno(), 0o600)
        json.dump(report, target, indent=2, allow_nan=False)
        target.write("\n")


def model_text(node):
    if isinstance(node, list):
        return "".join(model_text(child) for child in node)
    if not isinstance(node, dict):
        return ""
    if node.get("kind") == "text" and isinstance(node.get("text"), str):
        return node["text"]
    return "".join(model_text(child) for child in node.values())


def page_marker(body, identity, marker):
    model = json.loads(body)
    return isinstance(model, dict) and model.get("id") == identity and marker in model_text(model)


def self_test():
    require(proc_memory("VmRSS: 10 kB\nVmHWM: 20 kB\nRssAnon: 8 kB\n")["VmRSS"] == 10240, "self_test_proc")
    require(numeric_lines("usage_usec 7\nfile 9\n")["usage_usec"] == 7, "self_test_cgroup")
    try:
        safe_path("../escape")
    except AcceptanceError:
        pass
    else:
        raise AcceptanceError("self_test_path")
    pages = [{"id": str(i), "source": f"public/{i}.md", "path": f"pages/{i}.html", "data_path": f"_docs/pages/{i}.json", "public": True} for i in range(10)]
    private = {"path": "private/page.html", "data_path": "_docs/pages/private.json"}
    hot, batch = plans({"hot_pages": pages[:8], "pages": pages, "assets": [{"path": "assets/public/a.png"}], "private_page": private})
    require(len(hot) <= 64 and len(batch) == 256 and Counter(row[0] for row in batch) == {"owner": 128, "viewer": 128}, "self_test_plan")
    require(request_label("/_docs/api/v1/drafts/opaque/publish?token=not-recorded") == ("admin", "/_docs/api/v1/drafts/{id}/publish"), "self_test_diagnostic_redaction")
    records = [{"role": "owner", "category": "html", "path": "/synthetic.html", "elapsed_ms": value, "ok": value != 12} for value in range(1, 13)]
    partial = timing_summary(records)
    require(partial["completed_requests"] == 11 and partial["failed_requests"] == 1 and len(partial["slowest_requests"]) == 10
            and partial["slowest_requests"][0]["elapsed_ms"] == 12 and partial["request_elapsed_s"] == 0.078, "self_test_partial_diagnostics")
    fragmented = {"id": "private", "title": "IGNORE_ME", "blocks": [{"inlines": [
        {"kind": "text", "text": "PRIVATE_"}, {"kind": "text", "text": "RESOURCE_"}, {"kind": "text", "text": "SENTINEL"}]}]}
    encoded = json.dumps(fragmented).encode()
    require(page_marker(encoded, "private", "PRIVATE_RESOURCE_SENTINEL") and not page_marker(encoded, "other", "PRIVATE_RESOURCE_SENTINEL")
            and not page_marker(encoded, "private", "WRONG_MARKER") and "IGNORE_ME" not in model_text(fragmented), "self_test_semantic_page_text")
    print("Resource harness self-validation passed (no Docker resources created)")


def benchmark(report):
    report["runtime"] = local_docker()  # Before creation: never trust a remote engine's PID.
    head = command(["git", "rev-parse", "--verify", "HEAD"], check=False)
    dirty = command(["git", "status", "--porcelain"], check=False)
    if head.returncode == 0 and dirty.returncode == 0:
        report["runtime"].update({"git_head": head.stdout.decode().strip(), "git_dirty": bool(dirty.stdout.strip())})
    qa = load_module("cms_resource_client", "docker-cms.test.py")
    generator = load_module("cms_resource_fixture", "cms_resource_fixture.py")
    resources, sampler, measured = OwnedDocker(), None, None
    report["active_phase"] = "fixture_and_initialization"
    deadline = time.monotonic() + 900
    clients, latencies, timings, counts = {}, [], [], Counter()
    phase_offset, phase_started = 0, time.monotonic()
    try:
        with tempfile.TemporaryDirectory(prefix="pb-cms-resource-fixture-") as directory:
            staging = Path(directory)
            metadata = generator.build_fixture(staging)
            require(len(metadata["pages"]) == 512 and len(metadata["assets"]) == 128 and len(metadata["code_includes"]) == 128, "fixture_shape")
            require(sum(not page["public"] for page in metadata["pages"]) == 1, "fixture_privacy_shape")
            for page in metadata["pages"]:
                for key in ("source", "path", "data_path"):
                    safe_path(page[key])
            manifest = json.loads((staging / "docs.json").read_text())
            private = metadata["private_page"]
            private_title = next(page["title"] for page in manifest["pages"] if page["id"] == private["id"])
            forbidden = [private[key].encode() for key in ("id", "source", "path", "data_path", "sentinel")]
            forbidden += [value.encode() for value in (private_title, "private-resources", "Private resource group", "Private group metadata")]
            report["fixture"] = {key: metadata[key] for key in ("file_count", "source_bytes", "kind_counts", "search_target_bytes")}
            report["fixture"]["pages"], report["fixture"]["assets"] = 512, 128
            resources.create_volume()
            password = secrets.token_urlsafe(32).encode()
            resources.helper("bootstrap", "pb-md5-generator", ["admin-user", "--data-dir", "/data", "--username", "resource-owner", "--display-name", "Resource Owner", "--password-stdin"], password)
            with socket.socket() as listener:
                listener.bind(("127.0.0.1", 0))
                port = listener.getsockname()[1]
            origin = f"http://localhost:{port}"
            initial = resources.server("initialize", port, origin + "/bench", True)
            docker("start", initial)
            wait_ready(origin, min(deadline, time.monotonic() + 120))
            docker("stop", "--time=10", initial)
            resources.helper("fixture", "tar", ["-xf", "-", "-C", "/data/content"], fixture_tar(staging))
            measured = resources.server("measured", port, origin + "/bench", False)
            report["active_phase"] = "cold_process_start"
            cold_start = time.monotonic()
            docker("start", measured)
            info = resources.running(measured)
            report["runtime"].update({"image": IMAGE, "image_id": info["Image"]})
            sampler = Sampler(info)
            sampler.thread.start()
            wait_ready(origin, min(deadline, cold_start + 180))
            sampler.take()
            report["phases"].append({"name": "cold_process_start", **summary(list(sampler.samples), time.monotonic() - cold_start, [], 0)})

            class Client(qa.Client):
                def __init__(self, role):
                    super().__init__(origin, "/bench")
                    self.role = role

                def publication(self, path, body, expected):
                    class NoRedirect(urllib.request.HTTPRedirectHandler):
                        def redirect_request(self, request, stream, code, message, headers, target):
                            return None
                    headers = {"Content-Type": "application/json", "Origin": self.origin, "X-CSRF-Token": self.csrf,
                               "Cookie": "; ".join(f"{key}={value}" for key, value in self.cookies.items())}
                    request = urllib.request.Request(self.origin + self.prefix + path, data=json.dumps(body).encode(), headers=headers, method="POST")
                    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
                    try:
                        with opener.open(request, timeout=min(60, max(0.1, deadline - time.monotonic()))) as response:
                            require(response.status == expected, "publication_http_status")
                            return response.read((32 << 20) + 1)
                    except urllib.error.HTTPError:
                        raise AcceptanceError("publication_http_status") from None

                def request(self, path, method="GET", body=None, expected=200, parse=True, headers=None):
                    require(time.monotonic() < deadline, "overall_deadline")
                    started, ok = time.monotonic(), False
                    try:
                        if method == "POST" and path.startswith("/_docs/api/v1/drafts/") and path.endswith("/publish"):
                            data = self.publication(path, body, expected)
                        else:
                            data = super().request(path, method, body, expected, False, headers)
                        if method == "GET" and not path.startswith("/_docs/api/v1/"):
                            require(time.monotonic() - started <= 15, "reader_request_deadline")
                        require(len(data) <= 32 << 20, "http_response_limit")
                        if self.role == "viewer":
                            require(not any(token in data for token in forbidden), "scoped_private_data_leak")
                            if expected == 200 and path.startswith("/_docs/pages/"):
                                require(private["sentinel"] not in model_text(json.loads(data)), "scoped_private_data_leak")
                        result = json.loads(data) if parse and data else data if not parse else None
                        if isinstance(result, dict) and result.get("csrfToken"):
                            self.csrf = result["csrfToken"]
                        ok = True
                        return result
                    finally:
                        require(len(timings) < 4096, "request_diagnostic_limit")
                        elapsed_ms = (time.monotonic() - started) * 1000
                        category, synthetic_path = request_label(path)
                        latencies.append(elapsed_ms)
                        timings.append({"role": self.role, "category": category, "path": synthetic_path,
                                        "method": method, "elapsed_ms": round(elapsed_ms, 3), "ok": ok})
                        counts[f"{self.role}:{expected if ok else 'failed'}"] += 1
                        completed = len(timings) - phase_offset
                        if completed and completed % 64 == 0:
                            print(f"Resource progress: {report.get('active_phase', 'setup')} requests={completed} elapsed_s={time.monotonic() - phase_started:.3f}", flush=True)

            clients = {role: Client(role) for role in ("owner", "viewer")}

            def phase(name, operation, settle=False):
                nonlocal phase_offset, phase_started
                report["active_phase"] = name
                resources.running(measured, sampler.pid)
                before = sampler.take()
                offset, phase_offset, phase_started = len(sampler.samples), len(timings), time.monotonic()
                completed = False
                try:
                    operation()
                    if settle:
                        time.sleep(2)
                    require(not sampler.error, "host_sampler_failed")
                    sampler.take()
                    resources.running(measured, sampler.pid)
                    completed = True
                finally:
                    if not completed:
                        try:
                            sampler.take()
                        except Exception:
                            pass
                    with sampler.lock:
                        rows = list(sampler.samples[offset:]) or [before]
                    records = timings[phase_offset:]
                    entry = {"name": name, "completed": completed,
                             **summary(rows, time.monotonic() - phase_started, latencies[phase_offset:], before["cpu_stat"]["usage_usec"]),
                             **timing_summary(records)}
                    report["phases"].append(entry)
                    if completed:
                        report.pop("active_phase", None)
                    print(f"{'Completed' if completed else 'Incomplete'} resource phase: {name}, requests={len(records)}", flush=True)
                return entry

            def authenticate():
                clients["owner"].request("/_docs/api/v1/session", "POST", {"username": "resource-owner", "password": password.decode()})
                viewer_password = secrets.token_urlsafe(32)
                clients["owner"].request("/_docs/api/v1/users", "POST", {"username": "resource-viewer", "displayName": "Resource Viewer", "password": viewer_password,
                                                              "role": "viewer", "grants": {"read": [metadata["public_prefix"]], "write": []}}, expected=201)
                clients["viewer"].request("/_docs/api/v1/session", "POST", {"username": "resource-viewer", "password": viewer_password})

            phase("authentication", authenticate)
            password = b""
            def requests(plan):
                for role, path, status in plan:
                    body = clients[role].request(path, expected=status, parse=False)
                    if role == "owner" and path == safe_path(private["path"]):
                        require(private["sentinel"].encode() in body, "owner_private_fixture_missing")
                    if role == "owner" and path == safe_path(private["data_path"]):
                        require(page_marker(body, private["id"], private["sentinel"]), "owner_private_fixture_missing")
            phase("cold_process_full_512_pages", lambda: requests([("owner", safe_path(page["path"]), 200) for page in metadata["pages"]]))
            hot, batch = plans(metadata)
            edit = next(page for page in metadata["hot_pages"] if page["kind"] == "markdown")
            marker = "RESOURCE_PUBLICATION_SENTINEL"

            def models(after):
                sizes = {}
                for role, client in clients.items():
                    project = client.request("/_docs/project.json")
                    require(len(project["pages"]) == (512 if role == "owner" else 511), "project_page_count")
                    raw = client.request("/_docs/search.json", parse=False)
                    search = json.loads(raw)
                    require(len(search) == (512 if role == "owner" else 511), "search_page_count")
                    require((marker.encode() in raw) == after, "search_publication_visibility")
                    sizes[role] = len(raw)
                report["search_bytes_after" if after else "search_bytes_before"] = sizes

            phase("project_and_uncached_search_before", lambda: models(False))
            phase("hot_working_set", lambda: requests(hot), True)
            report["hot_working_set_entries"] = len(set(hot))

            def publish():
                owner = clients["owner"]
                page = owner.request("/_docs/api/v1/documents/" + edit["id"] + "?published=true")
                saved = owner.request("/_docs/api/v1/documents/" + edit["id"], "PUT", {"revision": page["revision"], "content": marker + "\n\n" + page["content"]})
                require(saved.get("published") is False and saved.get("draft"), "edit_must_stage")
                draft = saved["draft"]
                result = owner.request("/_docs/api/v1/drafts/" + draft["id"] + "/publish", "POST", {"revision": draft["revision"]})
                require(result.get("published") is True, "publication_failed")
                for client in clients.values():
                    require(marker.encode() in client.request(safe_path(edit["path"]), parse=False), "html_publication_visibility")
                    require(page_marker(client.request(safe_path(edit["data_path"]), parse=False), edit["id"], marker), "page_json_publication_visibility")

            batches = []
            for number in range(1, 7):
                batches.append(phase(f"mixed_256_batch_{number}", lambda: requests(batch), True))
                require(batches[-1]["requests"] == 256, "mixed_batch_request_count")
                if number == 3:
                    phase("explicit_edit_and_publication", publish)
                    phase("project_and_uncached_search_after", lambda: models(True))
            growth = batches[5]["rss_tail_median_bytes"] - batches[3]["rss_tail_median_bytes"]
            report["warm_plateau"] = {"baseline_batch": 4, "comparison_batch": 6, "rss_growth_bytes": growth,
                                      "tolerance_bytes": PLATEAU, "passed": growth <= PLATEAU}
            require(growth <= PLATEAU, "warm_rss_plateau_exceeded")
            require(not sampler.error, "host_sampler_failed")
            resources.running(measured, sampler.pid)
            report["request_counts"] = dict(counts)
            report["unexpected_http_errors"], report["oom_events"], report["restarts"] = 0, 0, 0
    finally:
        report["request_counts"] = dict(counts)
        report["request_attempts"] = len(timings)
        report["request_failures"] = sum(not row["ok"] for row in timings)
        if sampler is not None:
            sampler.close()
            with sampler.lock:
                report["last_numeric_samples"] = list(sampler.samples[-4:])
            if sampler.error:
                report["sampler_failure"] = sampler.error
        if measured is not None:
            try:
                info = resources.inspect(measured)
                if info is not None:
                    report["container_state"] = {"oom_killed": bool(info["State"].get("OOMKilled")),
                                                 "exit_code": int(info["State"].get("ExitCode", 0)), "restart_count": int(info.get("RestartCount", 0))}
            except Exception:
                report["container_state_unavailable"] = True
        report["cleanup_succeeded"] = resources.cleanup()
        require(report["cleanup_succeeded"], "owned_resource_cleanup_failed")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report", type=Path, help="write numeric phase summaries and samples; no credentials")
    parser.add_argument("--self-test", action="store_true", help="validate pure helpers without Docker or fixture generation")
    options = parser.parse_args()
    if options.self_test:
        self_test()
        return 0
    os.umask(0o077)
    def interrupted(signum, frame):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, interrupted)
    report = {"schema_version": 1, "status": "failed", "limits": {"memory_bytes": MEMORY, "memory_swap_total_bytes": MEMORY, "cpus": 1},
              "sampling_interval_ms": 500, "notes": [CAVEAT, "Search is measured before/after publication; responses above 2MiB are not reader-cache entries.",
                                                    "Reader latency includes curl process overhead (15s limit); explicit publication uses a 60s transport budget. No forced GC or host cache manipulation."], "phases": []}
    code = 1
    try:
        benchmark(report)
        report["status"], code = "passed", 0
    except BaseException as error:
        report["failure"] = str(error) if isinstance(error, AcceptanceError) else type(error).__name__
    if options.report:
        try:
            write_report(options.report, report)
        except Exception:
            print("Resource report write failed", file=sys.stderr)
            return 1
    print(json.dumps({key: report[key] for key in ("status", "limits", "warm_plateau", "failure", "cleanup_succeeded") if key in report}, allow_nan=False))
    return code


if __name__ == "__main__":
    raise SystemExit(main())
