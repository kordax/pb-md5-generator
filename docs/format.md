# Version 1 draft format

A documentation project has three JSON contracts: the authoring manifest, the compiled project index, and each compiled page. These are the **version 1 draft** of the pb-docs format. They describe the current portable model; they do not claim to be a final documentation standard or a complete validator for every imported API dialect.

Each contract uses [JSON Schema Draft 2020-12](https://json-schema.org/draft/2020-12). Download the standalone schemas below. Their `$ref` links stay inside the same file, so validation needs no project-specific schema server.

| Contract | Schema | Stable schema ID |
| --- | --- | --- |
| Authoring manifest | [manifest-v1.json](../document/schema/manifest-v1.json) | `urn:pb-docs:manifest:v1` |
| Compiled project index | [project-v1.json](../document/schema/project-v1.json) | `urn:pb-docs:project:v1` |
| Compiled page | [page-v1.json](../document/schema/page-v1.json) | `urn:pb-docs:page:v1` |

A compiled site also publishes these files at `_docs/schema/manifest-v1.json`, `_docs/schema/project-v1.json`, and `_docs/schema/page-v1.json`. Go integrations can call `document.JSONSchema("manifest")`, `document.JSONSchema("project")`, or `document.JSONSchema("page")`. `document.SchemaNames()` returns the supported names in sorted order. Each call returns independent data.

## Authoring a manifest {#manifest}

Save the manifest schema locally and point an editor at it with the optional `$schema` field. This field identifies the contract for tooling; it does not grant the compiler permission to fetch remote files.

```json
{
  "$schema": "schema/manifest-v1.json",
  "version": 1,
  "title": "Example documentation",
  "description": "Guides and API references",
  "pages": [
    {"id": "guide", "title": "Guide", "source": "guide.md", "path": "index.html"},
    {"title": "HTTP API", "source": "api/openapi.yaml", "path": "reference/http.html", "kind": "openapi"},
    {"title": "RPC API", "source": "api/service.proto", "path": "reference/rpc.html", "kind": "protobuf"}
  ],
  "assets": ["logo.png", "api/openapi.yaml"]
}
```

`version`, `title`, and a nonempty `pages` array are required. Each page needs `title`, `source`, and `path`. `kind` is `markdown`, `protobuf`, or `openapi`; leaving it out selects Markdown regardless of the source filename. `description`, `assets`, and page IDs are optional.

Sources and assets use clean relative paths. Declare one `index.html` homepage. Public page paths use lowercase relative `.html` paths; `_docs/` and `assets/` belong to the compiler. A declared asset is copied into the output asset namespace. Supported attachments include images, PDF and text files, and `.json`, `.yaml`, `.yml`, and `.proto` source contracts. Local page and asset links must refer to declared content.

An explicit page ID matches `[a-z0-9][a-z0-9._-]{0,127}`. Otherwise the compiler derives a stable ID from the public path. Supply an ID when a page should keep its identity across a path change.

## Reading compiled data

Start at `_docs/project.json`. Its `format` is `pb-docs/project`, its `version` is `1`, and its `root` is `../`. Each `pages` entry contains an ID, title, public path, source location, kind, and `data` path. Resolve `data` relative to the project index itself: `pages/abc.json` means `_docs/pages/abc.json`. Resolve public page paths relative to the site root identified by `root`.

Load only the page artifacts you need. A page has `format: "pb-docs/page"`, `version: 1`, its identifying metadata, and a required `blocks` array. Containers have recursive `children`; text content can contain recursive `inlines`. Block and inline `kind` values are enumerated by the page schema. Their fields describe headings, code, lists, tables, formatting, links, images, and other supported content without embedding the whole source Markdown in every node.

A local inline `reference` identifies a page, asset, or anchor independently of its rendered destination. Resolving that identity still requires the project and its ownership/link checks. A `source` location records `file` and, when known, one-based `line` and `column`. Generated content uses `generated: true` and omits original line coordinates.

## API content and exact numbers

API pages can include an `api` graph with operations, schemas, servers, security schemes, and preserved source documents. Required collections are JSON arrays, including empty arrays; they are not `null`. Operations carry effective servers and security after inheritance. A security array is an OR of requirements, while each requirement is an AND of schemes and scope arrays. An empty requirement permits anonymous use.

Schema nodes preserve their object or boolean `definition` and reference edges instead of expanding recursive types indefinitely. A reference target is resolved relative to the source document. A reference marked `dynamic` records a static fallback; it is not a full evaluation of JSON Schema dynamic scope.

`documents[].content`, `raw`, and other preserved JSON payloads intentionally accept protocol extensions and details beyond the typed envelope. Validating the page schema does not validate every embedded OpenAPI or JSON Schema keyword, follow an embedded `$ref`, or prove that a renderer exposes every retained feature. Unknown fields in typed objects are rejected; extension data belongs in the preserved payload containers.

JSON values retain their original numeric precision. Readers using an IEEE-754 number type should use a lossless JSON parser or the exact `exampleJSON`, `defaultJSON`, and `valueJSON` string companions where supplied. Rendering a companion directly preserves its value; parsing it through an ordinary floating-point JSON parser can round it again.

Protobuf annotation bounds are inclusive. Integer examples use `ceil(@min)` through `floor(@max)`, including either endpoint, and equal integer bounds produce that fixed value. Empty integer intervals are errors. Exact constraints can cover the complete signed and unsigned 64-bit ranges without rounding through a float.

## Structural and compiler conformance

The schemas check required fields, JSON types, known kinds, version tags, and recursive content shapes. Unknown format versions fail validation. Use a Draft 2020-12 validator with its built-in meta-schema, and register the downloaded schema locally when validating offline.

The compiler additionally checks clean portable paths, duplicate and case-folded names, IDs and output ownership, local links and anchors, source confinement, UTF-8 validity, and resource budgets. Titles allow at most 256 UTF-8 bytes and descriptions 4096 bytes. JSON Schema `maxLength` counts characters, so its limits are only an upper bound for those byte budgets. A structurally valid manifest can still fail compilation.

Schema validation alone does not establish link uniqueness, path safety, complete API semantics, or compliance with compiler resource limits. Successful schema validation and successful compilation are separate checks.
