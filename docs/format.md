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

`version`, `title`, and a nonempty `pages` array are required. Each page needs `title`, `source`, and `path`. `kind` is `markdown`, `html`, `protobuf`, or `openapi`; leaving it out selects Markdown regardless of the source filename. `description`, `assets`, and page IDs are optional.

Sources, assets and public output paths use clean relative paths of at most 4096 UTF-8 bytes. Declare one `index.html` homepage. Public page paths use lowercase relative `.html` paths; `_docs/` and `assets/` belong to the compiler. A declared asset is copied into the output asset namespace. Supported attachments include images, PDF and text files, and `.json`, `.yaml`, `.yml`, and `.proto` source contracts. Local page and asset links must refer to declared content.

An explicit page ID matches `[a-z0-9][a-z0-9._-]{0,127}`. Otherwise the compiler derives a stable ID from the public path. Supply an ID when a page should keep its identity across a path change.

## Portable appearance and behavior

The manifest can also declare `logo`, `favicon`, `footer`, `themeId`, a resolved `theme`, and the `navigation`, `editor`, `api`, and `publishing` groups. The compiled project index preserves these fields so another renderer can use the same settings. Brand images are project-relative raster assets that must appear in `assets`; remote image URLs, SVG, scripts and authored CSS are not theme instructions. In a compiled project, resolve `logo` and `favicon` as `assets/<source path>` relative to the project index's `root`.

A theme contains an ID, name, optional description, `mode` (`auto`, `light`, `dark`), a six digit hexadecimal `accent`, `font` (`system`, `serif`, `mono`), `density` (`comfortable`, `compact`), `width` (`normal`, `wide`), and a corner `radius` from 0 to 24 pixels. Optional `light` and `dark` palettes override `background`, `surface`, `text`, `muted`, `border`, and `accent`, each as `#RRGGBB`. The compiler creates an external stylesheet from these tokens; the reader does not execute arbitrary theme code or load remote fonts.

The builtins are `default`, `paper`, and `midnight`. A builtin ID can stand alone. A custom `themeId` requires complete resolved tokens in `theme`, with a matching ID. CMS theme files live at `themes/<id>.json`; applying a theme copies its tokens into the manifest. A static export therefore needs no CMS database or theme registry. See [appearance and settings](appearance.md) for the controls and their defaults.

`navigation` controls `showTOC`, `showPreviousNext`, and `stickyHeader`. `editor` selects `defaultFormat` and `previewOnSave`. `api` controls the request client, cURL display, a request timeout from 1000 to 30000 milliseconds, and a response preview limit from 1024 to 1048576 bytes. `publishing` selects `autoPublish` and `gitPushOnPublish`; these are CMS authoring policies, and automatic Git push requires Git storage. Static rendering preserves the authoring policies as metadata.

Omitting a group selects its standard defaults: visible table of contents and adjacent page links, a sticky header, Markdown editing without automatic preview, an enabled API client and cURL controls, a 30 second request timeout, a 1 MiB response preview, and explicit publication without automatic Git push. In authoring source, `theme`, `navigation`, `editor`, `api`, and `publishing` may also be `null`, with the same effect as omission. A theme's optional `light` and `dark` palettes follow this rule too. A supplied group uses its declared values; booleans omitted within that group are false.

The compiled project omits unset pointer groups and palettes rather than emitting `null`. CMS settings request bodies remain strict typed objects: explicit `null` is not an update command. Source-file decoding and HTTP update decoding are separate contracts.

## Explicit HTML documents

Choose `kind: "html"` for a document written in HTML. This path accepts formatting, lists, tables, disclosures, links and images through an allowlist sanitizer. Scripts, styles, frames, forms, SVG, MathML and event attributes are removed. Links and images use the same source-relative manifest resolution as Markdown, and external images require HTTPS. Explicit IDs must be unique and cannot use the reader's reserved namespace; headings receive stable anchors and appear in the table of contents.

An HTML page exports a block such as:

```json
{
  "kind": "html",
  "source": {"file": "guide.html"},
  "html": "<p>A sanitized paragraph.</p>",
  "text": "A sanitized paragraph."
}
```

`html` belongs only to an `html` block and contains sanitized markup; `text` is plain text for search. HTML source is bounded to 4 MiB, its DOM to 50000 nodes and 64 levels, and its rendered result to 16 MiB. Raw HTML encountered inside an ordinary Markdown page remains omitted with `omitted: true`; choosing the explicit source kind is required.

The structural schema cannot prove that an HTML string is safe. Readers consuming JSON from another producer must apply their own sanitizer before inserting its markup.

## Reading compiled data

Start at `_docs/project.json`. Its `format` is `pb-docs/project`, its `version` is `1`, and its `root` is `../`. Each `pages` entry contains an ID, title, public path, source location, kind, and `data` path. Resolve `data` relative to the project index itself: `pages/abc.json` means `_docs/pages/abc.json`. Resolve public page paths relative to the site root identified by `root`.

Load only the page artifacts you need. A page has `format: "pb-docs/page"`, `version: 1`, its identifying metadata, and a required `blocks` array. Containers have recursive `children`; text content can contain recursive `inlines`. Block and inline `kind` values are enumerated by the page schema. Their fields describe headings, code, lists, tables, formatting, links, images, and other supported content without embedding the whole source Markdown in every node.

A local inline `reference` identifies a page, asset, or anchor independently of its rendered destination. Resolving that identity still requires the project and its ownership/link checks. A `source` location records `file` and, when known, one-based `line` and `column`. Generated content uses `generated: true` and omits original line coordinates.

## Directive blocks

Callouts, tabs and includes are typed blocks. Their children use the same content model as ordinary Markdown; a reader can render them without executing author code or parsing generated HTML.

| Kind | Fields and structure |
| --- | --- |
| `callout` | Required `variant` (`note`, `info`, `tip`, `warning`, or `danger`) and a nonempty resolved `title`; optional content in `children` |
| `tabs` | One to 32 `tab` children; `id` identifies the rendered group |
| `tab` | A nonempty plain-text `title`, optional `id` and ordinary `children`; valid only directly inside `tabs` |
| `include` | Required `includeSource`, the resolved project-relative included filename; `children` contain its compiled content |

Titles count as plain text. An omitted callout title becomes its default label, such as `Warning`. The page schema limits titles to 256 characters; the compiler enforces the tighter limit of 256 UTF-8 bytes. `title`, `variant` and `includeSource` belong to their listed block kinds, and unknown typed fields are rejected.

Directive titles and include paths reject control characters. A language label matches `[A-Za-z0-9][A-Za-z0-9_+.-]{0,31}`. An include with a `language` contains exactly one `code` child. The child keeps the original text and language. With no language, the included Markdown becomes ordinary typed blocks, including nested directives. An empty Markdown file can produce an include without children. Includes retain their identity instead of flattening away the source boundary.

The include block's `source` records the directive in its containing document. Each included child records its original file and line when available. This distinguishes the place that requested an include from the content it brought in:

```json
{
  "kind": "include",
  "source": {"file": "guide.md", "line": 7, "column": 1},
  "includeSource": "snippets/request.json",
  "language": "json",
  "children": [
    {
      "kind": "code",
      "source": {"file": "snippets/request.json", "line": 1, "column": 1},
      "language": "json",
      "text": "{}\n"
    }
  ]
}
```

The compiler resolves includes locally with path confinement, cycle checks and shared resource limits. Schema validation checks their structural representation; it does not open included files or prove filesystem safety. See the [authoring guide](authoring.md) for the directive grammar.

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
