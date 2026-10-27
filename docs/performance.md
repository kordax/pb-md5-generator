# Performance baseline

Measured on 2026-10-27 with Go 1.26.10, Linux amd64 and an AMD Ryzen 9 9950X3D
(32 logical CPUs). These are local smoke measurements for the static compiler,
not a claim about every input, browser or API schema.

## Corpus and result

`BenchmarkBuild1000Pages` creates 1,000 Markdown sources of 4,337 bytes each,
a manifest and a complete static site. Each page contains one heading and a
repeated prose paragraph. Every generated page includes navigation to all pages.
The corpus contains no protobuf sources, images or external services.

The corpus SHA-256 is
`7fc38b6d3ea6d5d4bb07204d8cc60ed8d01a09be390c1e405e28ede1bce6b0df`.
It hashes source files and `docs.json` in lexical path order, with each path,
NUL, content and NUL included in the digest.

A compiled test binary measured separately from Go compilation reported three
single-iteration runs after adding typed page JSON exports:

```text
BenchmarkBuild1000Pages-32
1752857604 ns/op  926014536 B/op  25307455 allocs/op
1718783306 ns/op  925303952 B/op  25305448 allocs/op
1727555175 ns/op  925825680 B/op  25305525 allocs/op
peak_rss_kib=45656 elapsed_s=5.30 user_s=5.83 system_s=0.48
```

The 1.72 to 1.75 second builds include both HTML and a typed JSON artifact for
every page. Peak RSS was about 45 MiB. The approximately 926 MB of cumulative
allocations is churn across the whole build, not retained memory. Repeated
navigation rendering is a candidate for reducing that churn.

The earlier static-only measurement was 1.608 seconds, 914,633,024 B/op and
39,572 KiB peak RSS. It had fewer output artifacts. These small local samples
do not establish latency percentiles or a universal regression tolerance.

A separate repeat test built the two-page fixture 100 times. After GC, retained
heap differed by tens of KiB, goroutines remained at 2, and open descriptor counts
did not increase. The test allows a bounded heap fluctuation rather than requiring
an exact allocator result. Larger protobuf and browser memory corpora are still
needed.

## OpenAPI corpus

`BenchmarkBuildOpenAPI128Operations` builds one OpenAPI 3.1.0 page with 128 HTTP
operations, 128 object schemas and self-referential schema edges. Its generated
JSON source is 65,528 bytes, SHA-256
`e336bf9e1fd83bb252f1a3cb68cbcb0df97a0ed9c500d405c217467ba97c408b`.
The build includes specification validation, normalization, static API reference,
search, typed JSON and UI assets. The benchmark warms the bounded specification
validator cache before timing; process accounting still includes that warm-up.

```text
BenchmarkBuildOpenAPI128Operations-32
57443206 ns/op  88213480 B/op  527444 allocs/op
54026571 ns/op  88192056 B/op  527377 allocs/op
54097631 ns/op  88285864 B/op  527405 allocs/op
peak_rss_kib=43344 elapsed_s=0.37 user_s=0.59 system_s=0.07
```

Warm builds took 54 to 57 ms with about 42 MiB process peak RSS. This is a
moderate synthetic API, not a result for every schema graph or a cold-start
latency guarantee. A separate 100-build test runs the real importer, renderer and
JSON export, then checks bounded retained heap, goroutines and file descriptors.
Protobuf and browser corpora remain additional work.

## Regression cases

Tests also cover inputs that are small on disk but expensive to expand:

- Heading ID generation remembers the next suffix for each repeated heading.
  The 1,000/8,000-heading benchmark took approximately 1.2/11.2 ms in a single
  run after fixing the former quadratic search.
- Pre-parse structural budgets, AST/heading limits, bounded HTML writers and
  project-wide link limits reject excessive expansion before publication.
- Search text and saved URL fragments are detached from their backing strings.
  Before the fix, 16 two-MiB pages retained about 30 to 39 MiB even when metadata
  was truncated or normalized to a short string. Tests exercise long text,
  a one-character normalized result and a small fragment after a long query.
- Repeated renderer use replaces its output rather than appending prior content.
  Recursive example generation has explicit depth and work limits.

The initial core reader CSS and JavaScript were about 3.3 KiB gzip, excluding
page content, search data, images and the optional API assets. They use no framework
runtime, CDN or background polling. Browser rendering, accessibility and
interaction latency still require a real browser verification pass.

## Repeat the measurements

Use the pinned project toolchain:

```console
GOTOOLCHAIN=go1.26.10 go test ./internal/site -run '^$' -bench . -benchmem
GOTOOLCHAIN=go1.26.10 go test ./internal/site -run TestRepeatedBuildResources -v -count=1
GOTOOLCHAIN=go1.26.10 go test ./internal/site -run TestRepeatedAPIBuildResources -v -count=1
GOTOOLCHAIN=go1.26.10 go test -race ./internal/site -run TestPageMetadataDoesNotRetainLargeBackingStrings -count=1
```

For peak RSS, compile the test binary once, then measure its execution with the
operating system's process accounting tool. Do not include Go compilation in the
compiler runtime result. Record the corpus, toolchain, machine and raw output
when comparing future changes.

The [design](design.md#resource-and-correctness-contracts) lists the broader
performance gates and measurements still required before completing the system.
