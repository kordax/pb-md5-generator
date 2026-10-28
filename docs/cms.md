# Run the documentation CMS

The CMS runs the documentation reader, editor and administration panel in one Go process. The Docker image includes the editor assets, Git, an SSH client and CA certificates. Docker with the Compose plugin is enough on the host.

## Docker startup

Run these commands from the project root. If the previous static preview occupies port `8080`, stop it with `docker compose --profile static stop docs`. Build the CMS image first:

```sh
docker compose build cms
```

Create the first administrator before starting the server. This Bash example keeps the password out of command arguments and shell history:

```bash
docker compose stop cms
IFS= read -r -s -p 'Administrator password: ' cms_password
printf '\n'
printf '%s' "$cms_password" | docker compose run --rm -T cms admin-user \
  --username owner \
  --display-name Owner \
  --password-stdin
unset cms_password
```

Choose a unique passphrase of at least 15 Unicode characters and at most 1024 UTF-8 bytes. `--password-stdin` reads until EOF and uses the exact bytes received. `printf '%s'` sends no newline. Leading and trailing spaces are preserved; newline characters are rejected by the account password policy. There is no default account or password. `admin-user` creates the first account once; manage additional users and permissions in the administration panel.

Start or update the CMS:

```sh
docker compose up --build -d --wait
```

Open <http://localhost:8080/_docs/admin/> and sign in. The documentation reader is at <http://localhost:8080/> and uses the same account and access rules.

The startup flags have separate purposes: `--build` rebuilds the image from the current source, `-d` leaves the container running in the background, and `--wait` waits for its health check. The health check reports that the control plane is ready; account creation is a separate step.

The Taskfile provides the same startup and shutdown operations:

```sh
task docker:up
task docker:down
```

`cms:up` and `cms:down` are aliases. `docker compose down` stops and removes containers while retaining the named data volume. `task docker:down` also handles the optional static and maintenance profiles. A later `up` reuses the accounts, settings, sources, drafts, history, Git repositories and publication. Removing a data volume, including through `down -v`, deletes that stored data.

Useful operations:

```sh
docker compose ps
docker compose logs -f cms
docker compose stop cms
docker compose start cms
```

For a different local port, keep the setting for subsequent commands:

```sh
export DOCS_PORT=8082
docker compose up --build -d --wait
```

The default public URL becomes `http://localhost:8082`. Put persistent deployment settings in a local `.env` file if they need to survive a new shell. The image runs as UID/GID `65532`, with a private `/data` volume, a read-only root filesystem and a temporary `/tmp`. Git SSH credentials live under `/data/ssh`, exposed to the SSH client through its home directory. They are included in full data backups and are never copied into the image.

The default host binding is `127.0.0.1`. Behind an HTTPS reverse proxy, set `CMS_PUBLIC_URL` to the exact public URL, including a hosting prefix if needed, such as `https://docs.example.test/team`. Preserve the public Host header and route the complete prefix to the container. The administration panel then lives at `/team/_docs/admin/`. Use `DOCS_BIND` if the container needs another host binding. Local plain HTTP requires the literal `localhost` hostname; other public hostnames require HTTPS.

The default CMS container listens on port `8090` internally. Images are pinned by digest, and the compiler uses Go `1.26.10`. The runtime uses [Debian Bookworm's supported LTS release](https://www.debian.org/releases/bookworm/). Refresh base image pins and rebuild to apply upstream maintenance updates.

## Persistent data

The named data volume contains:

- `content/`: managed source files, attachments and the Git working tree when enabled.
- `state/auth/`: private account and session state.
- `state/content/`: private drafts, revision history, storage connections and recovery journals.
- `site/`: the current compiled publication.
- `ssh/`: optional private Git SSH identity and verified host keys.

The default volume names follow the Compose project name, normally `pb-md5-generator_data` and `pb-md5-generator_backups`. `CMS_DATA_VOLUME` and `CMS_BACKUP_VOLUME` can select explicit names. A changed project name selects a different set of default volumes, as described in [Docker's project naming rules](https://docs.docker.com/compose/how-tos/project-name/).

## Backup and restore

Backups are offline snapshots of the complete data volume, including Git metadata, unpublished drafts, users, sessions, themes, settings and SSH credentials. Keep exported archives private. The maintenance containers do not have network access, and backup mounts the data volume read-only.

This task stops the CMS, writes a timestamped archive and checksum into the separate private backup volume, then starts the CMS again:

```sh
task cms:backup
```

For a known archive name:

```sh
CMS_BACKUP_FILE=before-upgrade.tar.gz task cms:backup
```

An existing archive is never replaced. If backup fails, the task leaves the CMS stopped so the error can be inspected. Restart it with `docker compose up -d --wait cms` after resolving the problem.

The equivalent commands are:

```sh
docker compose stop cms
docker compose --profile maintenance run --rm --no-deps backup
docker compose up -d --wait cms
```

The archive command also acquires the same source, state, account and publication locks used by the application. A live CMS or account command makes it fail instead of copying inconsistent state. Initialize the CMS once before its first backup. Git storage must be a self-contained clone inside `content/.git`; linked worktrees and external alternate object directories require their own migration procedure.

Restore always uses a new empty data volume. Keep the original volume for rollback, and select the archive name printed by the backup task:

```sh
export CMS_DATA_VOLUME=pb-docs-restored-data
export CMS_BACKUP_FILE=before-upgrade.tar.gz
task cms:restore
```

The task stops the CMS, checks the archive checksum and paths, restores the fresh volume and starts the CMS with it. A non-empty target is rejected. Record the selected `CMS_DATA_VOLUME` in the local `.env` file so future commands keep using the restored volume. The previous volume remains available until it is explicitly removed.

### Export an archive

Copy completed archives to private storage outside Docker so a lost Docker host does not also lose its backups. This Bash example exports an archive and its checksum without printing their contents:

```bash
cms_backup_dir="$HOME/.local/share/pb-docs-backups"
mkdir -p "$cms_backup_dir"
chmod 700 "$cms_backup_dir"
docker compose --profile maintenance create --no-build backup
cms_backup_container="$(docker compose --profile maintenance ps --all -q backup)"
docker cp "$cms_backup_container:/backup/before-upgrade.tar.gz" "$cms_backup_dir/before-upgrade.tar.gz"
docker cp "$cms_backup_container:/backup/before-upgrade.tar.gz.sha256" "$cms_backup_dir/before-upgrade.tar.gz.sha256"
docker compose --profile maintenance rm -f backup
chmod 600 "$cms_backup_dir/before-upgrade.tar.gz" "$cms_backup_dir/before-upgrade.tar.gz.sha256"
```

To import that pair into a backup volume on another host, use the same source checkout and build the CMS image, then stream each file into the private backup volume:

```bash
export CMS_BACKUP_FILE=before-upgrade.tar.gz
cms_backup_dir="$HOME/.local/share/pb-docs-backups"
docker compose --profile maintenance run --rm --no-deps -T --entrypoint sh backup \
  -c 'umask 077; set -C; cat > "/backup/$CMS_BACKUP_FILE"' \
  < "$cms_backup_dir/$CMS_BACKUP_FILE"
docker compose --profile maintenance run --rm --no-deps -T --entrypoint sh backup \
  -c 'umask 077; set -C; cat > "/backup/$CMS_BACKUP_FILE.sha256"' \
  < "$cms_backup_dir/$CMS_BACKUP_FILE.sha256"
```

Then select a new `CMS_DATA_VOLUME` and run `task cms:restore`. Imported files keep the container user's ownership and private permissions. Restore rejects malformed checksums, unsafe paths, symbolic links and hard links before extracting the archive.

## Native binary

Build from the project root:

```sh
mkdir -p build
go build -o build/pb-md5-generator ./cmd/pb-md5-generator
```

Use a dedicated data directory outside the development checkout. Create the first administrator with the same stdin protocol:

```bash
cms_data="$HOME/.local/share/pb-docs"
IFS= read -r -s -p 'Administrator password: ' cms_password
printf '\n'
printf '%s' "$cms_password" | ./build/pb-md5-generator admin-user \
  --data-dir "$cms_data" --username owner --display-name Owner --password-stdin
unset cms_password
```

Start the process:

```sh
./build/pb-md5-generator serve \
  --data-dir "$HOME/.local/share/pb-docs" \
  --listen 127.0.0.1:8090 \
  --public-url http://localhost:8090
```

Open <http://localhost:8090/_docs/admin/>. Stop with Ctrl+C or SIGTERM. On Unix, the data directory must be private to the running user. On Windows, provision a private NTFS ACL. Only one CMS process can own a managed source, state or publication location at a time.

`--listen` controls the bind address; `--public-url` controls the address and origin used by the application. Without `--data-dir`, the CMS uses `pb-docs` in the operating system's user configuration directory. `--init=false` requires an existing managed content directory instead of creating the initial page. Run `serve --help` or `admin-user --help` for startup flags.

## Storage and synchronization

The default source is local storage. Administrators with read and write access to the content root can manage it in **Storage**. The server keeps connection settings privately under `state/content/`; they are separate from `docs.json`, document Git history and theme settings. Full data backups include them.

To start Git history for existing local documents, select Git in Storage, choose the initial branch and save. This creates a local initial commit without contacting a remote. It preserves source files, the published site, local revision history and saved drafts. The project revision changes; a saved draft stays available until its author explicitly reviews and rebases it. Enrollment is a one-way operation in this version: returning the same data directory to local storage is unsupported. Send this new history to an empty repository or a new branch. To continue an existing repository history, [start from a dedicated clone](#start-from-an-existing-clone).

Add up to eight named connections. Each name starts with an ASCII letter or digit and contains at most 64 letters, digits, underscores or hyphens. Names are unique without regard to case; reserved Windows device names are rejected. A URL can be an SSH repository such as `git@example.test:team/docs.git`, an `ssh://` URL, or a local/file path visible to the server. URLs are at most 2048 UTF-8 bytes. HTTP(S) transport, embedded passwords, URL queries, fragments and control characters are unsupported.

Select one connection as the fetch/pull source, or leave it empty when the repository only keeps local history or sends commits elsewhere. Independently enable the connections that should receive a requested push. They share one local history. Saving connections and opening Storage do not fetch or push; **Check connection** and **Push** explicitly contact the selected remote. The panel keeps a separate result and retry state for each connection.

A failed push retains the local commit and publication. Other targets can still succeed, and retrying the failed target sends the retained commit without creating another document commit. Push never forces a divergent remote. Branch selection and fetch/pull remain in **Publishing** and use the selected pull source. Resolve divergence explicitly rather than discarding documents. Configuration updates protect both the storage settings revision and the project revision; stale forms cannot overwrite a newer configuration.

### SSH credentials

Provide a dedicated SSH identity and verified `known_hosts` for the running account before checking or pushing an SSH connection. In the CMS image, those files belong under `/data/ssh`, owned by UID `65532`, with directory mode `0700` and private identity mode `0600`. SSH runs without interactive prompts and requires a matching verified host key. Private keys are supplied on the server and are never returned by the API or placed in document sources.

For a new container identity, these Bash commands stream already provisioned files into the private volume without putting their contents in command arguments. Existing destination files are not replaced:

```bash
docker compose exec -T cms sh -c \
  'umask 077; mkdir -p /data/ssh; set -C; cat > /data/ssh/id_ed25519' \
  < "$HOME/.ssh/pb-docs-deploy-key"
docker compose exec -T cms sh -c \
  'umask 077; mkdir -p /data/ssh; set -C; cat > /data/ssh/known_hosts' \
  < "$HOME/.ssh/pb-docs-known-hosts"
```

Provision those host keys through your trusted deployment process. An identity that needs an interactive passphrase cannot be unlocked by the CMS. Executable Git configuration, credential prompts and repository hooks are rejected. The complete backup contains SSH identities, so keep exported archives private.

### Start from an existing clone

Before the first CMS startup, prepare a dedicated self-contained clone in `<data-dir>/content`, containing a valid `docs.json`. Select `--storage git` for the native process or `CMS_STORAGE=git` in Compose. The initial remote defaults to `origin`; `--git-remote` or `CMS_GIT_REMOTE` selects another portable connection name. Keep the clone separate from the development checkout.

Startup flags seed the first storage configuration. Once a configuration is saved, it takes precedence across restarts; manage later connection changes in Storage. Do not edit recovery journals or replace Git metadata while the CMS runs. A transaction interrupted before its settings commit is rolled back; a committed transaction is completed on restart. Unexpected changed metadata fails closed and remains available for investigation. A missing storage configuration after enrollment also stops startup; restore the complete data backup rather than silently replacing the connection policy.

## Static example and publishing

The static example remains available separately:

```sh
task docker:static
```

Open <http://localhost:8081/>. To use the previous static port while the CMS is stopped:

```sh
STATIC_DOCS_PORT=8080 docker compose --profile static up --build -d --wait docs
```

For a standalone static output directory:

```sh
./build/pb-md5-generator build --config path/to/docs.json --output path/to/site
```

Static publication is independent of CMS accounts and access rules. Use the CMS reader when documents need per-user permissions. See [static deployment](deployment.md) for hosting and the separate `site` and `cli` image targets.
