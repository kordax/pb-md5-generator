# Project instructions for Claude

Read [SPARC.md](SPARC.md) before continuing. It records the full objective, the completed baseline, the current checkpoint, verification evidence and the remaining work. User-facing operation is documented in [docs/cms.md](docs/cms.md).

## Authorized scope and stopping point

The user requested completion of execution stage 1, file-operation reliability and publication recovery, followed by this handoff and a pause. Stages 2–9 are future work. Do not start them without a new request to continue. The overall CMS objective is not complete merely because stage 1 is complete.

Check the acceptance status in SPARC.md and the current Git tree. A draft or a local commit does not prove that its checks passed or that it was pushed or deployed.

## Working rules

- Work in `main`. Do not create implementation branches or merge unrelated historical branches. A detached worktree for verification is allowed.
- Keep other people's edits and local scratch files. Never delete ignored files to make checks pass.
- Use Gortex MCP to read, search and edit indexed source, including known files. Prefer a direct symbol or bounded file window. Use the repository/worktree view explicitly when needed. Follow its guarded edit workflow and applicable AGENTS.md instructions.
- Shell commands are appropriate for Git, builds, package installation, lint, tests and status. Do not use shell reads or writes to bypass Gortex for indexed source.
- A daemon restart can reset the MCP session. Reorient with `workspace.info` and retry a read-only call. Do not restart the daemon, remove its store or re-track the repository to expose a checkout.
- Never print, log or commit passwords, tokens, session cookies, CSRF values, SSH identities or decrypted credentials.
- Do not wait for GitHub Actions. Run the repository's checks locally before pushing. Keep the 90% total coverage gate and all security/lint settings intact. Report failures accurately.
- On 28 October 2026 the user explicitly authorized Playwright whenever useful, revoking the earlier browser ban. Use fresh isolated browser contexts and respect the incognito preference; never reuse the user's regular browser profile or credentials. An initial Impeccable reader critique is recorded in `.impeccable/critique/`; it is not full CMS browser acceptance.
- Use short commits such as `content: recover interrupted publications`, normally without a body. Keep one coherent change per commit.

## Toolchain and verification

The repository currently requires Go `1.26.10`; use that version rather than the general new-project default. The application uses Uber Fx modules. Go tests use Testify, with `require` for prerequisites and `assert` for independent checks.

Canonical local checks from the repository root:

```sh
env GOTOOLCHAIN=go1.26.10 task --force verify integration-test
env GOTOOLCHAIN=go1.26.10 task docker:test
```

These cover the repository's lint/security commands, unit and race tests, coverage, documentation/site checks, Node tests and integration scenarios. Docker acceptance is a separate command. Do not describe a command as passing until its process exits successfully.

Useful focused checks:

```sh
env GOTOOLCHAIN=go1.26.10 go test -race ./internal/studio/content
env GOTOOLCHAIN=go1.26.10 go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./internal/studio/content/...
npm --prefix internal/studio/web test
npm --prefix internal/studio/web run check
npm --prefix internal/studio/web audit --audit-level=low
gitleaks git --pre-commit --staged --redact=100 --no-banner
```

Scan the actual outgoing commit range with Gitleaks as well. A final push requires the applicable checks for that change, not only the focused examples above.

This workstation has a pre-existing ignored `tmp/main.go` scratch file. Repository-wide Go commands can include ignored Go source. Preserve it and perform final verification in a clean detached worktree containing the exact committed candidate. Do not add exclusions or security suppressions for the scratch file. Do not assume an old `/tmp` checkout still exists or matches `HEAD`.

## Run the CMS

```sh
task docker:up
# Stop containers while preserving the data volume:
task docker:down
```

`task docker:up` always invokes `docker compose up --build -d --wait cms` and prints reader/admin links after the health check passes. The port comes from the running container, so a configured `DOCS_PORT` is reflected in both links. Task aliases are `task cms:up` / `task cms:down`. The default administration panel is <http://localhost:8080/_docs/admin/>. Plain local HTTP requires the literal `localhost` hostname. The reader uses the same permissions.

The data volume holds accounts, sources, drafts, history, Git and SSH state. Do not use `down -v` for an ordinary restart. Follow docs/cms.md for offline backups and fresh-volume restore.

There is no default account or password. The first administrator is created with the offline `admin-user` command and a user-entered secret on stdin. The minimum is **10 Unicode characters**, the maximum is 1024 UTF-8 bytes. Stop the CMS before account bootstrap so the one-off command can obtain its state lock.

`docker compose run` does **not** support `--no-build`. Use `docker compose run --rm --no-deps -T cms admin-user ... --password-stdin`. `docker compose up` does support `--no-build`; it is useful after building a reviewed image from the clean verification checkout. Never rebuild a preview from unrelated unaccepted working-tree changes.

## Invariants to preserve

- A project revision includes source inventory and storage branch/HEAD. Draft ownership and draft revision are independent concurrency checks. Never silently replace another draft or discard newer local edits.
- Page identity, source path and output URL are separate. Property and source changes save together with explicit revision protection. Reviewed rebase remains explicit.
- Keep publication backups and the previous site until their recorded authority is verified. Unknown files, symlinks, history, branch or HEAD must stop recovery without deleting evidence.
- History moves only after the durable `published` phase. Rollback checks the old history before touching live source/output.
- Cleanup first renames a verified private stage/output directory to a sibling `.retired-*` path, then removes its contents. Keep interrupted retired remainders for operator inspection; do not overwrite a collision or add unconditional garbage collection by filename prefix.
- A missing transaction directory can represent completed cleanup only when the final live source, output and history match the journal. An existing directory with a missing plan or owner is not that case.
- Startup publication recovery must not create Git commits, push or reset HEAD. A commit without a recorded receipt can require operator review.
- Content reads and writes are blocked while an incomplete publication requires recovery. Do not bypass that guard through reader caches or new endpoints.
- Do not call appearance reads from inside a content `View` / `ReadView` callback: appearance reads acquire content access and can deadlock.
- Keep bounded file, history, output and request processing. Preserve existing root ownership, symlink, path, CSRF and authorization checks.
