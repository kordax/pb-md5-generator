---
version: 1
slug: "internal-site-ui-go"
primary_target: "internal/site/ui.go"
related_targets: ["internal/site/api_view.go","internal/site/api_style.go","internal/studio/web/assets/studio.css","internal/studio/web/index.html"]
---

# Reference Workspace

Public documentation and API reference use Read mode; Studio uses Operate mode. Readers find a guide or operation, understand it and deliberately try a request. Authors edit, review and publish without losing work. Keep authorization, theme settings, Git workflows and request safety intact.

## Direction contract

THESIS: One documentation workspace connects reading, API exploration and publishing. Replace the discarded oversized blue card interface with the user's chosen Reference Workspace.

OWN-WORLD: Restrained white and zinc, violet accent, fine rules and small corners. DM Sans for readable UI, IBM Plex Mono for code, served locally. Daylight reading stays light; the API request and response pane is dark.

STORY: Grouped navigation locates real content. The central column explains it. A distinct sample pane makes a request understandable before the visitor explicitly sends it; Studio uses the same vocabulary for authoring.

FIRST VIEWPORT: A 68px top bar joins documentation, API and Studio. A 224px grouped rail anchors the left; API details occupy the readable center, and a roughly 36% dark right pane holds working request and response controls. Guides use that space for a quiet contents rail. Studio has a compact navigation rail, task heading and flat workspace. On mobile these become accessible disclosures and one readable column.

FORM: Reference Workspace, option 2, selected by the user from all three preserved concepts. Match .impeccable/mocks/decision/reference-workspace.html. SEED REFERENCE: seed:0bc1a3c22af13ae7. Signature interaction: explicit API request editing and Send beside its reference, with honest live versus sample response state.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
