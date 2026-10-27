# Administration assets

The CMS serves `Assets()` below `/_docs/admin/`. Its API is the sibling
`../api/v1/`, so the interface also works below an HTTP path prefix. Published
reader assets have no dependency on this package or its editor bundle.

The Go binary embeds `assets/`. Node is a build and test dependency, not a server
or deployment dependency. Commit generated `assets/editor.js` and its legal
notices with changes to `src/editor.mjs`, the package manifest or lockfile.

```sh
npm ci --ignore-scripts
npm run build
npm run check
npm test
npm run security
```

`check` recompiles the CodeMirror bundle in memory and verifies its exact bytes.
It never fixes stale assets. The dependency versions and lockfile are pinned;
security checks include development dependencies.

The administration index contains one `__PB_DOCS_CSP_NONCE__` placeholder. The
HTTP handler replaces it with a fresh random nonce on each response and includes
that nonce in its `style-src` policy. CodeMirror receives it through
`EditorView.cspNonce`; scripts stay external and local. CodeMirror also updates
layout through CSS style properties. Browser CSP and layout behavior still need
actual browser acceptance tests; Node DOM tests do not prove them.

Session cookies belong to the HTTP server. JavaScript uses same-origin Fetch,
keeps its CSRF token in memory, clears password fields before requests and never
writes credentials to browser storage. Fetch responses, source files, uploads
and concurrent requests have explicit limits. One document editor and preview
request are active at a time. File loads and document loads ignore stale results.

The preview iframe permits same-origin assets while withholding script, form,
popup and top-navigation permissions. Its child policy explicitly blocks scripts
and permits only local images, fonts and styles (plus data images). Author content is never assigned
to the application DOM as HTML. The isolated preview receives its own document
through `srcdoc`.

Tests cover the API/state boundaries and actual application event handlers with
a small DOM implementation. They cover dirty drafts, load races, failed saves,
revisions, permissions, password clearing, settings, files and repository actions.
They are not browser, visual, accessibility or cookie-compatibility verification.
