# API reference and requests

API pages share the same navigation, search and deployment as ordinary guides.
Choose `protobuf` or `openapi` explicitly in the manifest. The compiler exports a
[typed page model](format.md) alongside the static reference; the reader does not
need an API server to display it.

## Import OpenAPI

Declare a local JSON or YAML contract:

```json
{"id":"catalog","title":"Catalog API","source":"api/catalog.yaml","path":"reference/catalog.html","kind":"openapi"}
```

The importer validates OpenAPI 3.0.x, 3.1.x and 3.2.0 against the corresponding
specification schema before normalization. It preserves operations, parameters,
media types, responses, server variables, security alternatives, schemas and
examples. Local `$ref` files resolve relative to the referring contract, inside
the manifest directory. JSON Pointer and named schema anchors are supported;
recursive references remain links. Dynamic references retain their authored
meaning and a marked static fallback instead of being recursively expanded.
Remote references and undeclared network access are rejected.

A schema-valid contract can still describe an API incorrectly. Validate its
examples and actual server behavior in that API's own tests. This documentation
compiler does not contact a server or prove implementation conformance.

The [Catalog example](../testdata/integration/catalog.openapi.yaml) demonstrates
path/query parameters, a request body, responses and an API key. Its example
server is a placeholder. The source format and API version are distinct in the
model: `openapi: 3.1.0` selects the contract dialect; `info.version` describes the
API release.

## Import protobuf

Protobuf pages expose messages, enums, fields, services, RPC input/output types
and streaming flags. They also retain the established generated Markdown and
JSON/XML examples in a source reference disclosure. A gRPC method is displayed as
an RPC; it does not acquire an invented HTTP endpoint or a browser Send button.

`@ignore` removes a declaration and its descendants from the reference and the
machine-readable descriptor export. A public field may still name a hidden type,
but its shape stays opaque. An unavailable imported type also stays opaque. The
standalone source parser does not resolve import declarations; supplying a
complete descriptor request to the importer is a separate library entry point.

Protobuf schema IDs and OpenAPI schema IDs are stable references, not recursively
copied trees. Sources retain their relative file names and available locations;
generated Markdown blocks are explicitly marked as generated.

## Read operations and schemas

Operation and schema anchors support direct links. API pages include parameter,
request, response and authentication information, with expandable schema and raw
contract previews. A preview labels any truncation; the page JSON contains the
complete accepted bounded model. Exact numeric examples also have JSON text
companions so browser number parsing does not silently change an int64 or uint64
example.

The current static preview limits each schema's property list and raw JSON text.
A fully interactive schema explorer, selectable client libraries and per-operation
search ranking remain part of the [UI roadmap](design.md#ui-behavior).

## Try an HTTP request

Select **Try request** on a normal HTTP operation. The panel loads the local page
model on demand. Choose a server and its variables, enter parameters, choose a
request media type and review the generated URL, headers, body and cURL command.
Only **Send request** contacts the API. Opening a page, searching or expanding a
schema does not send API requests.

An absolute HTTP(S) server is required. For a relative server URL, enter an
explicit API base URL; the documentation host is not assumed to be the API host.
Redirects are rejected; select the final API URL explicitly. Authentication supports pasted API keys, Basic credentials and bearer tokens.
The panel shows security alternatives and required combinations. Credentials
stay in panel memory and are cleared when it closes or the page is left. They
are not saved to browser storage or generated output.

Browser CORS rules apply. The client omits ambient cookies and does not add a
proxy. Cookie authentication, OAuth login, mutual TLS, binary/multipart uploads
and non-HTTP transports need another client; the panel reports unsupported
request forms instead of guessing an encoding. GET and HEAD cannot send a body.
Webhooks and callbacks describe incoming operations and do not receive Send
controls.

The request timeout is 30 seconds and cancellation aborts the active request.
Request and response bodies are limited to 1 MiB; response reads are streamed and
stop at the limit. The panel keeps one active operation and one bounded page
model. These limits protect the reader; they are not API server limits.

## Import limits

Each contract is limited to 4 MiB; one page may load at most 64 local documents
and 16 MiB in total. Adapter limits also bound nesting, nodes, operations, schemas
and references. The exported page JSON is limited to 16 MiB before encoding.
Oversized input produces a build error and preserves the previous published
site. Split a large reference into separate pages when it exceeds these limits.

No browser or live server compatibility claim follows from the unit tests alone.
The release plan includes visual, keyboard, CORS and real request cancellation
checks in a supported browser.
