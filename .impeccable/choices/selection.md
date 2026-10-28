# Frontend design decision

On 28 October 2026 the user approved all three concepts for preservation and selected **2. Reference Workspace** for implementation: «Все хорошие, запомни их, но я выбрал 2».

1. [Unified Canvas](../mocks/decision/unified-canvas.html): a calm documentation-first layout with grouped navigation and an emerald accent.
2. [Reference Workspace](../mocks/decision/reference-workspace.html), **selected**: a flat white and zinc interface, violet accent, grouped navigation, a readable central reference, and a dark request/response pane.
3. [Publishing Desk](../mocks/decision/publishing-desk.html): an editorial documentation and publishing workspace with a two-level masthead.

Keep all three original HTML concepts. Do not overwrite the approved sketches when implementing the product. They are design proposals, not the running application; their example controls and data do not promise additional product functionality.

The chosen direction applies to public documentation, API/OpenAPI reference and the CMS/admin interface. Keep the existing content, permissions, editor, Git workflows and theme customization. The finished product must support responsive layouts, accessible keyboard interaction and self-contained assets.

Native Impeccable decision: `a4f81893`, option `reference-workspace`, code-led build, no build-path flip and no additional steer. Surface-composition seed: `seed:0bc1a3c22af13ae7`.
