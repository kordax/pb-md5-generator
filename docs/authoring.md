# Write documentation

A project contains a JSON manifest and local sources. The manifest chooses the
navigation order and output paths. Markdown is the default page format; protobuf
and OpenAPI are explicit importers for API reference. Each build also exports a
[typed project/page model](format.md) for other readers and tooling.

## Describe the project

Create `docs.json` next to your source tree:

```json
{
  "version": 1,
  "title": "Acme documentation",
  "description": "Guides and API reference",
  "pages": [
    {"title": "Overview", "source": "docs/index.md", "path": "index.html"},
    {"title": "Operations", "source": "docs/operations.md", "path": "operations.html"},
    {"title": "API", "source": "api/service.proto", "path": "reference/api.html", "kind": "protobuf"}
  ],
  "assets": ["images/overview.png"]
}
```

Version, project title and pages are required. Each page requires a title, source
and destination. An omitted kind means `markdown`; `protobuf` and `openapi` select
API adapters. An optional `id` is unique, uses 1 to 128 lowercase ASCII letters,
digits, dots, underscores or hyphens, and starts with a letter or digit. Give a
page an explicit ID to preserve its identity when its output path changes;
otherwise the ID is derived from that path. Exactly one page must own
`index.html`; destinations must be unique, lowercase relative paths ending in
`.html`. The `_docs/` and `assets/` directories are reserved for generated and
listed assets. Source paths use forward slashes, including on Windows.

Unknown fields, duplicate JSON members and unsupported versions are errors.
Version 1 is a draft contract with a published [JSON Schema](format.md#manifest).
The broader content standard is still being specified in the [design](design.md).
Export an editor schema with `pb-md5-generator schema -kind manifest -o docs.schema.json`.

## Use familiar Markdown

CommonMark with tables, task lists, strikethrough, autolinks and definition lists
is supported. Use language names on fenced code blocks:

```go
func example() string { return "hello" }
```

Headings create page anchors and a contents list. Unicode headings are supported.
Repeated headings receive a numeric suffix, such as `installation-1`.
For a stable explicit anchor, write `## Installation {#install}`.
Do not use the reserved `_docs-` prefix for an anchor.

Raw HTML is omitted. The exact empty anchors emitted by the legacy protobuf
Markdown generator are retained as safe IDs; other raw elements and attributes
are not enabled. The builder does not execute JSX, templates, scripts or plugins.

## Link sources and assets

Write a relative link to another declared source, with an optional heading
fragment. The builder rewrites it to that page's HTML path. For example, this
[deployment guide](deployment.md#publish-the-output) is a normal Markdown link to
its source. Relative `.html` links to declared output pages also work.

Declare every downloadable file or image in `assets`, then link it using its
source-relative path. Assets are copied beneath `assets/` while preserving their
source paths. Supported extensions are PNG, JPEG, GIF, WebP, AVIF, ICO, PDF, TXT,
JSON, YAML and protobuf source files.
For example, a source at `docs/index.md` can use `../images/overview.png` when
`images/overview.png` is declared.

Missing pages, assets and fragment targets fail the build. Source-relative links
keep the project portable below any hosting prefix. Use an absolute HTTP(S) URL
for an external site and `mailto:` for email links. No external content is fetched
during compilation. Remote HTTPS images may be loaded by the browser when used.

## Import protobuf reference

Set `kind` to `protobuf` for a page's `.proto` source. The site uses the same
strict annotation parser, deterministic example generator and renderer as the
standalone `generate` and `lint` commands. Imported dependency files are not
required or downloaded. References to unavailable external types remain plain
code rather than broken links.

```protobuf
// @title: Accounts API

// Creates an account.
// @autocode[json]
message CreateAccount {
  // @doc type=email example="alice@example.com"
  string email = 1;
  repeated string tags = 2;
}
```

Generated examples handle repeated fields, maps, nested messages and oneof
choices. Recursive references are omitted or represented as empty containers;
example expansion is bounded. JSON and XML examples have matching code fence
languages. The existing JSON example envelope with `trx` and the message name is
preserved for compatibility. Exact inclusive numeric constraints and comment
marker rules are described in the [syntax contract](syntax.md).

## Import HTTP API reference

Set `kind` to `openapi` for a local JSON or YAML contract. OpenAPI 3.0.x, 3.1.x and
3.2.0 are validated before importing. Local referenced contracts stay inside the
project tree; remote references are rejected. The reference displays operations,
parameters, responses, security requirements and schemas. **Try request** opens a
bounded HTTP client that sends only after an explicit action. See the
[API guide](api.md) for authentication, CORS and supported request forms.

## Limits and diagnostics

The first compiler accepts up to 2,048 pages and 256 listed assets. It bounds the
manifest at 1 MiB, each source or generated page at 4 MiB, each asset at 16 MiB,
and encoded search data at 8 MiB. It limits each page to 8,192 headings, 50,000 AST
nodes and 16 MiB of rendered HTML; project link metadata is capped at 100,000
entries and 8 MiB of anchor/fragment text. A pre-parse limit of 50,000 structural
punctuation characters and line breaks bounds potential AST expansion. That
conservative count also includes punctuation inside code blocks; split a page
when it exceeds the budget. Markdown nesting is limited to 64 levels. Page-model
JSON is limited to 16 MiB before encoding; [API imports](api.md#import-limits) have
additional document and graph limits. The compiler processes pages sequentially
and retains only bounded link metadata and search text between pages.

Search indexes each page title and the first 32 KiB of normalized page text.
It is intended for small and medium documentation projects; heading-level
ranking and a larger indexed search engine remain planned.

Paths cannot escape the manifest directory through traversal or symlinks.
Invalid input produces a command error and leaves an existing successful site
intact. Check mode reports missing or changed output without writing files.
