# Changelog

## [v1.1.1] - 2026-07-25

Maintenance release with release automation and reproducible project checks.

### Added

- Added automated release archives for Linux, macOS, and Windows on `amd64`
  and `arm64`, with SHA-256 checksums.
- Added direct renderer coverage for generated HTML anchors.
- Added regression checks for generated file and directory permissions.

### Changed

- Pinned local CI and security tools to explicit versions.
- Updated the Go toolchain and test workflow to Go 1.26.5.
- Generated Markdown files now use mode `0644`; generated directories use
  mode `0755`.
- Documented ready-to-run archives on the GitHub Releases page.

## [v1.1.0] - 2026-07-11

Changes since `v1.0.0`.

### Added

- Added direct parsing of `.proto` source files without invoking `protoc` or
  `protoc-gen-go`.
- Added repeatable `-I` / `-proto-path` options for additional local protobuf
  source roots.
- Added `-split-by-package` mode, which writes a root index and a separate
  `README.md` for every protobuf package.
- Added links between types declared in different generated package documents.
- Added support for remote or otherwise unresolved protobuf imports: imported
  files no longer have to be downloaded to generate documentation.
- Added recursive source discovery while preserving nested source paths.

### Changed

- `-d` / `-proto-dir` now defines the primary protobuf source root.
- `-f` / `-files` accepts semicolon-separated files that may be absolute,
  relative to the current directory, or relative to a configured source root.
- Files declaring the same protobuf package are combined into one package
  document when `-split-by-package` is enabled.
- Prefix content supplied with `-p` is prepended to every generated package
  document.
- Building from source now requires Go 1.26 or newer; downloaded binaries have
  no external runtime dependencies.

### Removed

- Removed the runtime dependency on `protoc` and `protoc-gen-go`.

[v1.1.1]: https://github.com/kordax/pb-md5-generator/compare/v1.1.0...v1.1.1
[v1.1.0]: https://github.com/kordax/pb-md5-generator/compare/v1.0.0...v1.1.0
