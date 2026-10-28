# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Readers use guides, reference documentation and interactive API examples. Authors and administrators write, review and publish documentation and manage its files, users, permissions, themes and storage. The user confirmed that the emphasis is the whole documentation product, rather than API reference alone.

## Product Purpose

Provide a complete documentation solution: a documentation site and the CMS used to manage it. General documentation and API reference belong to the same product. MkDocs, Scalar and Wiki.js informed the original scope; the latest explicit design brief combines Mintlify-like product documentation with Scalar-like API presentation.

## Operating Context

A Go compiler produces portable static documentation and runs the authenticated CMS. The CMS uses persistent local files and optional Git synchronization. Markdown, sanitized HTML, protobuf and OpenAPI are implemented inputs. The administration editor uses JavaScript modules, CodeMirror 6 and esbuild. Node is a build/development tool, not a production server requirement.

## Capabilities and Constraints

Preserve existing content, stable anchors, permissions, drafts, editor state, history, Git operations, theme customization and explicit API execution. API page loads must not send requests or persist credentials. Preserve low CPU/memory goals and static deployment. The broader complete CMS goal includes unfinished functionality; do not claim complete feature parity with reference products.

Development must run through `task dev` in Docker, with no automatically restarted services, no additional database daemon and no persistent native development server. Private development data may reside in isolated Docker volumes. Playwright is explicitly authorized whenever useful, using isolated browser contexts.

## Brand Commitments

The user explicitly rejected the old appearance and its spacing-only refinement. The new frontend must look materially different: flatter, contemporary, precise and cohesive across documentation, API reference and administration. Mintlify is the reference for the overall documentation experience; Scalar is the reference for API pages. These references set the craft level rather than licensing copied assets or invented features. Product naming and authored project branding remain configurable.

## Evidence on Hand

Existing documentation and API fixtures, CMS workflows, `docs/design.md`, the full goal in `SPARC.md`, and the initial reader critique in `.impeccable/critique/` establish functionality and current shortcomings. The previous refinement is a local checkpoint, not an approved new visual identity.

## Product Principles

- Documentation and its management form one coherent product.
- Content and useful API examples lead; internal compiler metadata stays secondary.
- Familiar controls, clear navigation and deliberate spacing support reading and authoring.
- Visual changes preserve data integrity, access controls and existing author customization.
- A design direction is shown and chosen before it replaces the current frontend.
