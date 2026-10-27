# Deploy a site

The build output is a directory of ordinary static files. Deploy that directory
to an existing web server or static hosting provider. Reading the site requires
no Go process, Node.js application, database or external font service.

## Run the complete example with Docker {#docker}

Install Docker with the Compose plugin, then run from the repository root:

```console
docker compose up --build -d --wait
```

Open <http://localhost:8080/>. The image contains the complete example project:
guides, syntax and format specifications, protobuf reference, and an OpenAPI page
with the HTTP request panel. Go, Node.js and a database are not needed on the host.
The first build downloads the base images and Go modules; later builds reuse them.

The example HTTP contract uses `api.example.test` as a placeholder. To send a real
request, choose your own API server in the panel. That server must allow browser
CORS requests from the documentation origin.

Useful commands:

```console
docker compose ps
docker compose logs -f docs
docker compose stop
docker compose start
docker compose down
```

`stop` keeps the container for the next `start`; `down` removes this Compose
project's container and network. The built image remains available. After editing
sources, rebuild and replace the container with the same start command:

```console
docker compose up --build -d --wait
```

The image is a snapshot of the documentation at build time. Source edits are not
watched automatically. To choose another port:

```console
DOCS_PORT=8081 docker compose up --build -d --wait
```

Then open <http://localhost:8081/>. On PowerShell, set `$env:DOCS_PORT = "8081"`
before running Compose. Use the same setting for later Compose commands.

The default listener is bound to `127.0.0.1`. A server installation can set
`DOCS_BIND` to its desired host address, or put the localhost listener behind an
existing HTTPS reverse proxy. The container listens on port `8080` internally.

## Build and move the site image

Compose builds the `site` target and tags it `pb-docs:local`. The equivalent
standalone build is:

```console
docker build --target site -t pb-docs:local .
```

For your own documentation, place the manifest and its sources inside the build
context. The default manifest is `docs.json`; select another with:

```console
docker build --target site --build-arg DOCS_CONFIG=manual/docs.json -t my-docs:1 .
```

Compose accepts the same choice through `DOCS_CONFIG`. Source and asset paths
remain relative to that manifest's directory.

An image can be moved to another Docker host without copying Go or the sources:

```console
docker save -o pb-docs-image.tar pb-docs:local
# On the destination host:
docker load -i pb-docs-image.tar
docker run -d --name pb-docs --restart unless-stopped \
  --read-only --tmpfs /tmp:rw,noexec,nosuid,size=16m \
  --cap-drop ALL --security-opt no-new-privileges:true \
  -p 127.0.0.1:8080:8080 pb-docs:local
```

Build the image for the destination CPU architecture. The final image contains
NGINX and generated static files. It runs as UID `101`, uses one worker, and needs
only a temporary writable `/tmp`. Compose sets a 128 MiB memory limit and a
health check; application data volumes are unnecessary. `/healthz` reports `ok`
when the server is running. The reader's API requests go directly from the browser
to the chosen API server; the container does not proxy them.

The base images are pinned by digest in `Dockerfile`. Refresh those pins when
updating Go or NGINX, then rebuild and verify. The
[upstream NGINX image documentation](https://github.com/nginx/docker-nginx-unprivileged)
explains its unprivileged port and temporary-path conventions.

## Run the compiler in Docker

The separate `cli` target contains only the standalone binary:

```console
docker build --target cli -t pb-docs-cli:local .
docker run --rm pb-docs-cli:local -help
```

On Linux or macOS, mount a project and run as the host user so generated files
keep the expected ownership:

```console
docker run --rm --user "$(id -u):$(id -g)" \
  --mount "type=bind,source=$PWD,target=/work" \
  pb-docs-cli:local build -config docs.json -o site

docker run --rm --user "$(id -u):$(id -g)" \
  --mount "type=bind,source=$PWD,target=/work" \
  pb-docs-cli:local lint -d testdata/integration -f full_api.proto
```

The same image supports `generate`, `build`, `lint` and `schema`. Use a read-only
mount for commands that do not write, and a writable mount for generated output.
The CLI image does not serve HTTP; the `site` target is the ready-to-run reader.

## Build and verify without Docker

Build the compiler once, then generate the site:

```console
task build
./build/pb-md5-generator build -config docs.json -o site
./build/pb-md5-generator build -config docs.json -o site -check
```

Sources are relative to the manifest. The output directory is relative to the
command's current working directory. Use absolute paths when running from a
scheduler or a different checkout. The seed defaults to `1`; use `-seed` to select
a different deterministic example fixture.

The output directory must be missing, empty or owned by a previous build. The
`_docs/build.json` inventory identifies generated files. Do not place hand-written
files inside the generated directory: unexpected files or directories cause a
build error so the compiler cannot silently delete them.

Successful builds replace the previous generated file set and remove stale pages.
Compilation happens in a staging directory next to the output, so validation
failures preserve the previous site. The output cannot contain source files or
replace the manifest directory or one of its parents.

## Preview locally

Use any available static HTTP server. For example, when Python is installed:

```console
python3 -m http.server 8080 --bind 127.0.0.1 --directory site
```

Open `http://127.0.0.1:8080/`. Reading and navigation also work when HTML files are
opened directly, but browsers restrict fetching the search index and API page
models over `file:`. Use HTTP to enable search and the request panel.
Clipboard copying requires a secure browser context such as HTTPS or localhost.

## Publish the output

Copy the entire output directory, including `_docs/` and any `assets/` files.
Preserve paths and MIME types for HTML, CSS, JavaScript and JSON. Local build files
are owner-readable by default; the upload or release-install step should assign
ownership and read permissions to the process that serves the site. Put private
content behind your host's normal access controls; static compilation does not
add reader authentication.

All generated navigation and UI asset URLs are relative. A site can be served at
`/`, `/docs/` or `/team/product/` without rebuilding or configuring an SPA fallback.
Pages use explicit `.html` filenames, so direct links work on ordinary static
hosts. Keep the trailing slash on the site's root URL.

Serve HTML and the search/build JSON files with revalidation rather than a long
immutable cache lifetime. UI filenames are stable in this first version, so
cache them with revalidation too. Upload a complete release to a new directory
and switch the serving path after upload to keep readers on a consistent release.

The generated HTML includes a restrictive Content Security Policy. Hosts can
also set equivalent response headers. The reader uses local CSS and JavaScript,
does not run author HTML, and makes no background polling requests. Search loads
its local index only after input. HTTP API pages load the local page model when
the request panel opens and contact a chosen API only after **Send request**.
Only these interactive pages allow HTTP(S) connections in their CSP. If a host
sets a stricter policy, allow the required API origins explicitly. Authentication
and CORS remain the API server's responsibility; see the [API guide](api.md).

## CI and releases

Run the repository checks with the pinned toolchain before publishing:

```console
GOTOOLCHAIN=go1.26.10 task verify
GOTOOLCHAIN=go1.26.10 task site
```

The standalone generator's existing release archives target Linux, macOS and
Windows on amd64 and arm64. The HTML output is independent of the operating system
that built it. Cross-platform site publication tests and an optional built-in
preview server remain part of the [distribution roadmap](design.md#verification-and-delivery-sequence).
