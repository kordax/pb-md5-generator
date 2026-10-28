"""Deterministic offline inputs for the CMS resource acceptance workload.

build_fixture writes only into an existing, empty, caller-owned staging directory.
The harness installs that staging tree into its stopped disposable CMS volume;
this module never reads or writes CMS ownership markers, auth state, or remotes.
"""

from __future__ import annotations

import hashlib
import json
import os
import posixpath
import stat
import struct
import zlib
from pathlib import Path
from typing import Any


PRIVATE_RESOURCE_SENTINEL = "PRIVATE_RESOURCE_SENTINEL"
PAGE_SOURCE_BYTES = 8192
CODE_SOURCE_BYTES = 2048
ASSET_BYTES = 512 * 1024
SEARCH_TARGET_BYTES = 6 * 1024 * 1024
KIND_COUNTS = {"markdown": 320, "html": 64, "protobuf": 64, "openapi": 64}


def _json(value: Any) -> bytes:
    return (json.dumps(value, ensure_ascii=True, indent=2, sort_keys=True) + "\n").encode("ascii")


def _fill(size: int, seed: str) -> bytes:
    if size < 0:
        raise ValueError("fixture prefix exceeds its byte budget")
    token = seed.encode("ascii")
    return (token * ((size + len(token) - 1) // len(token)))[:size]


def _padded(prefix: str, suffix: str, size: int, seed: str) -> bytes:
    start, end = prefix.encode("ascii"), suffix.encode("ascii")
    return start + _fill(size - len(start) - len(end), seed) + end


def _chunk(kind: bytes, data: bytes) -> bytes:
    return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)


def _png(index: int) -> bytes:
    """A valid one-pixel RGBA PNG, padded by a CRC-protected tEXt chunk."""
    signature = b"\x89PNG\r\n\x1a\n"
    header = _chunk(b"IHDR", struct.pack(">IIBBBBB", 1, 1, 8, 6, 0, 0, 0))
    pixels = _chunk(b"IDAT", zlib.compress(bytes((0, index, 255 - index, 96, 255)), level=9))
    end = _chunk(b"IEND", b"")
    text_size = ASSET_BYTES - len(signature) - len(header) - len(pixels) - len(end) - 12
    text = b"fixture\x00" + _fill(text_size - 8, f"resource-image-{index:03d} ")
    result = signature + header + _chunk(b"tEXt", text) + pixels + end
    if len(result) != ASSET_BYTES:
        raise ValueError("fixture PNG has an unexpected byte size")
    return result


def _open_empty_root(root: Path) -> int:
    if os.open not in os.supports_dir_fd or not hasattr(os, "O_NOFOLLOW"):
        raise ValueError("fixture generation requires confined directory operations")
    absolute = Path(os.path.abspath(root))
    for component in (absolute, *absolute.parents):
        if component.is_symlink():
            raise ValueError("fixture staging directory must not contain symlinks")
    descriptor = os.open(absolute, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        info = os.fstat(descriptor)
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid():
            raise ValueError("fixture staging directory must belong to the caller")
        if os.listdir(descriptor):
            raise ValueError("fixture staging directory must be empty")
        return descriptor
    except BaseException:
        os.close(descriptor)
        raise


def _write(root: int, name: str, data: bytes) -> None:
    """Create a new confined file without following or replacing any links."""
    parts = name.split("/")
    if any(part in ("", ".", "..") for part in parts) or name.startswith("/"):
        raise ValueError("invalid fixture path")
    current = os.dup(root)
    try:
        for part in parts[:-1]:
            try:
                os.mkdir(part, mode=0o700, dir_fd=current)
            except FileExistsError:
                pass
            child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=current)
            os.close(current)
            current = child
        descriptor = os.open(parts[-1], os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600, dir_fd=current)
        with os.fdopen(descriptor, "wb") as output:
            output.write(data)
    finally:
        os.close(current)


def _code(index: int) -> tuple[str, str, bytes]:
    extension, language = (
        ("go", "go"), ("js", "javascript"), ("ts", "typescript"), ("sh", "sh"),
        ("sql", "sql"), ("css", "css"), ("xml", "xml"), ("json", "json"),
    )[index % 8]
    name = f"public/examples/code-{index:03d}.{extension}"
    if extension == "json":
        schema = {
            "type": "object", "required": ["id", "name"],
            "properties": {"id": {"type": "integer", "minimum": 0}, "name": {"type": "string", "maxLength": 128}},
            "description": f"Local resource schema {index:03d}. ",
        }
        missing = CODE_SOURCE_BYTES - len(_json(schema))
        schema["description"] += _fill(missing, "Deterministic local schema text. ").decode("ascii")
        return name, language, _json(schema)
    if extension == "xml":
        data = _padded(f'<fixture id="{index:03d}"><value>', "</value></fixture>\n", CODE_SOURCE_BYTES, "Offline resource example. ")
    elif extension == "sh":
        data = _padded(f"#!/bin/sh\nprintf '%s\\n' 'fixture-{index:03d}'\n# ", "\n", CODE_SOURCE_BYTES, "Inert source example. ")
    elif extension == "sql":
        data = _padded(f"SELECT {index} AS fixture_id;\n/* ", " */\n", CODE_SOURCE_BYTES, "Inert source example. ")
    elif extension == "css":
        data = _padded(f".fixture-{index:03d} {{ color: #123456; }}\n/* ", " */\n", CODE_SOURCE_BYTES, "Inert source example. ")
    else:
        prefix = {
            "go": f'package fixture\n\nfunc Example{index:03d}() string {{ return "offline" }}\n// ',
            "js": f'export function example{index:03d}() {{ return "offline"; }}\n// ',
            "ts": f'export function example{index:03d}(): string {{ return "offline"; }}\n// ',
        }[extension]
        data = _padded(prefix, "\n", CODE_SOURCE_BYTES, "Inert source example. ")
    return name, language, data


def _protobuf(index: int) -> bytes:
    prefix = f'''syntax = "proto3";
package resource.fixture{index:03d};

// @title: Resource API {index:03d}
// Reads one local fixture item.
// @autocode[json]
message ReadRequest {{
  string id = 1;
  repeated string labels = 2;
}}

// The deterministic fixture response.
message ReadResponse {{
  string id = 1;
  string name = 2;
  bool active = 3;
  map<string, string> labels = 4;
}}

service ResourceService {{
  rpc Read(ReadRequest) returns (ReadResponse);
}}

// '''
    return _padded(prefix, "\n", PAGE_SOURCE_BYTES, "Offline protobuf source padding. ")


def _openapi(index: int) -> bytes:
    reference = f"../examples/code-{(index % 16) * 8 + 7:03d}.json"
    schema = {"$ref": reference}
    contract = {
        "openapi": "3.1.0",
        "info": {"title": f"Resource HTTP API {index:03d}", "version": "1.0.0", "description": "Local offline resource fixture. "},
        "paths": {
            f"/fixture-{index:03d}/items": {
                "get": {
                    "operationId": f"listFixture{index:03d}", "summary": "List local fixture items",
                    "responses": {"200": {"description": "Fixture items", "content": {"application/json": {"schema": {"type": "array", "items": schema}}}}},
                },
                "post": {
                    "operationId": f"createFixture{index:03d}", "summary": "Describe creating a local fixture item",
                    "requestBody": {"required": True, "content": {"application/json": {"schema": schema}}},
                    "responses": {"201": {"description": "Fixture item", "content": {"application/json": {"schema": schema}}}},
                },
            },
        },
    }
    missing = PAGE_SOURCE_BYTES - len(_json(contract))
    contract["info"]["description"] += _fill(missing, "Deterministic API description with no external server. ").decode("ascii")
    return _json(contract)


def build_fixture(root: Path) -> dict[str, Any]:
    """Build deterministic inputs, returning JSON-safe relative-path metadata.

    The directory must already exist and be empty. Invalid roots are rejected
    before writes. A later I/O failure leaves its partial staging tree for the
    caller to inspect or discard; existing files are never replaced or deleted.
    """
    descriptor = _open_empty_root(Path(root))
    pages: list[dict[str, Any]] = []
    assets: list[dict[str, Any]] = []
    includes: list[tuple[str, str]] = []
    file_count = source_bytes = 0

    def put(name: str, data: bytes) -> None:
        nonlocal file_count, source_bytes
        _write(descriptor, name, data)
        file_count += 1
        source_bytes += len(data)

    def page(identity: str, title: str, source: str, output: str, kind: str, data: bytes, public: bool = True) -> None:
        put(source, data)
        data_path = "_docs/pages/" + hashlib.sha256(identity.encode("ascii")).hexdigest()[:32] + ".json"
        pages.append({"id": identity, "title": title, "source": source, "path": output, "kind": kind, "public": public, "data_path": data_path})

    try:
        for index in range(128):
            name, language, data = _code(index)
            put(name, data)
            includes.append((name, language))
        for index in range(128):
            name = f"public/assets/image-{index:03d}.png"
            data = _png(index)
            put(name, data)
            assets.append({"source": name, "path": "assets/" + name, "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest()})
        for index in range(320):
            private = index == 319
            source = "private/resource.md" if private else ("public/index.md" if index == 0 else f"public/markdown/page-{index:03d}.md")
            output = "private/resource.html" if private else ("index.html" if index == 0 else f"guides/markdown-{index:03d}.html")
            identity = "resource-private" if private else ("resource-home" if index == 0 else f"markdown-{index:03d}")
            title = "Private resource page" if private else f"Resource guide {index:03d}"
            base = posixpath.dirname(source)
            prefix = f"# {title}\n\n[Home]({posixpath.relpath('public/index.md', base)})\n\n"
            prefix += (PRIVATE_RESOURCE_SENTINEL + "\n\n") if private else f"PUBLIC_RESOURCE_PAGE_{index:03d}\n\n"
            if index < len(includes):
                name, language = includes[index]
                prefix += f':::include source="{posixpath.relpath(name, base)}" language="{language}"\n:::\n\n'
                prefix += f"![Local fixture image]({posixpath.relpath(assets[index]['source'], base)})\n\n"
            prefix += ':::note title="Offline fixture"\nThese inputs require no external service.\n:::\n\n## Details\n\n'
            page(identity, title, source, output, "markdown", _padded(prefix, "\n", PAGE_SOURCE_BYTES, f"Resource guide {index:03d} contains deterministic local documentation text. "), not private)
        for index in range(64):
            prefix = f'<h1>Resource HTML {index:03d}</h1>\n<p><a href="../index.md">Home</a></p>\n<h2>Safe content</h2>\n<ul><li>Local source</li><li>No scripts</li></ul>\n<pre><code>fixture({index});</code></pre>\n<p>'
            page(f"html-{index:03d}", f"Resource HTML {index:03d}", f"public/html/page-{index:03d}.html", f"guides/html-{index:03d}.html", "html", _padded(prefix, "</p>\n", PAGE_SOURCE_BYTES, f"Safe HTML fixture {index:03d} uses local text only. "))
        for index in range(64):
            page(f"protobuf-{index:03d}", f"Resource protobuf {index:03d}", f"public/protobuf/api-{index:03d}.proto", f"reference/protobuf-{index:03d}.html", "protobuf", _protobuf(index))
        for index in range(64):
            page(f"openapi-{index:03d}", f"Resource HTTP API {index:03d}", f"public/openapi/api-{index:03d}.json", f"reference/openapi-{index:03d}.html", "openapi", _openapi(index))

        groups = []
        for kind in KIND_COUNTS:
            children = [{"kind": "page", "pageId": item["id"]} for item in pages if item["kind"] == kind and item["public"]]
            groups.append({"kind": "group", "id": "public-" + kind, "title": "Public " + kind, "description": "Deterministic local resource fixture", "children": children})
        private_page = next(item for item in pages if not item["public"])
        groups.append({"kind": "group", "id": "private-resources", "title": "Private resource group", "description": "Private group metadata", "collapsed": True, "children": [{"kind": "page", "pageId": private_page["id"]}]})
        manifest = {
            "version": 1, "title": "CMS resource fixture", "description": "Deterministic offline mixed documentation project.",
            "pages": [{key: item[key] for key in ("id", "title", "source", "path", "kind")} for item in pages],
            "assets": [item["source"] for item in assets],
            "navigation": {"showTOC": True, "showPreviousNext": True, "stickyHeader": True, "tree": {"unlistedPages": "omit", "items": groups}},
        }
        put("docs.json", _json(manifest))
        return {
            "version": 1, "manifest": "docs.json", "pages": pages,
            "hot_pages": [item for item in pages if item["public"]][:8],
            "private_page": {**private_page, "sentinel": PRIVATE_RESOURCE_SENTINEL},
            "assets": assets, "code_includes": [name for name, _ in includes],
            "file_count": file_count, "source_bytes": source_bytes,
            "kind_counts": dict(KIND_COUNTS), "search_target_bytes": SEARCH_TARGET_BYTES,
            "public_prefix": "public",
        }
    finally:
        os.close(descriptor)
