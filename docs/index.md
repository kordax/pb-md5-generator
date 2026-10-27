# Documentation that stays with your code

Write guides, tutorials, runbooks and architecture notes in Markdown. Keep API
reference next to the source that defines it. Build both into the same portable
site with one binary.

## Build this example

To view the complete example with Docker, run from the repository root:

```console
docker compose up --build -d --wait
```

Open <http://localhost:8080/>. See [Docker deployment](deployment.md#docker) for
ports, rebuilding, stopping and using your own sources.

To build without Docker:

```console
task build
./build/pb-md5-generator build -config docs.json -o site
```

The `site/` directory contains the complete reader: pages, styles, a small script
and a local search index. Deploy it with any static HTTP server. A Node.js server,
database and external CDN are unnecessary.

To check a previously built site without writing files:

```console
./build/pb-md5-generator build -config docs.json -o site -check
```

## Start with the content

- [Write documentation](authoring.md) explains the project manifest, Markdown,
  links, assets and protobuf imports.
- [Deploy a site](deployment.md) covers static hosting, subpaths and repeatable
  builds.
- [API reference and requests](api.md) explains local OpenAPI import and the HTTP
  request panel. [Protobuf](../testdata/integration/full_api.proto) and
  [HTTP](../testdata/integration/catalog.openapi.yaml) examples use the same reader.
- [Protobuf syntax](syntax.md) defines annotation, numeric and literal rules.
- [Document format](format.md) describes the typed exports and JSON Schemas.
- [Performance baseline](performance.md) records compiler measurements and resource
  regression tests.
- [Design and roadmap](design.md) defines the direction of the documentation
  standard and the remaining UI and syntax work.

## Available today

The reader includes responsive navigation, a page contents list, Unicode heading
anchors, local page search, light and dark themes, and copying code on secure
origins. Reading and navigation work without JavaScript. Search loads only when
a reader enters a query.

The compiler validates declared page and asset links, including heading fragments,
and reports broken references before publishing. It keeps the previous site if
compilation fails. Identical inputs and example seeds produce identical files.

## Next product milestones

The static reader is the foundation of a broader documentation system. Grouped
navigation, versions and languages, content directives and a fuller API explorer
are still planned. The design records
their acceptance criteria rather than treating this first reader as full
MkDocs or Scalar parity.
