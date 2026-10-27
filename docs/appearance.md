# Appearance and site settings

The CMS stores site settings with the documentation sources. A theme and a title change pass through the same validation, publication and Git workflow as a page edit. A static export keeps the resolved appearance, so readers do not need a running CMS to display it.

## Brand and reading layout

In **Settings**, set the site title, description and footer. The title allows 256 UTF-8 bytes, the description 4096, and the footer 1024. These values are plain text. The reader escapes markup in them.

Upload a raster image as an attachment before selecting it as the logo or favicon. Enter its source path, such as `images/logo.png`, and declare it in the project's assets. PNG, JPEG, GIF, WebP, AVIF and ICO are supported. Remote URLs and SVG are not accepted as brand images.

The reading controls show or hide the table of contents and adjacent page links, and choose whether the header remains visible while scrolling. The theme controls the font, spacing, reading width and corner radius. The font choices use local system, serif or monospace families; no remote font service is required.

## Themes

The builtins are **Default**, **Paper** and **Midnight**. Duplicate a builtin to create a custom theme. Each custom theme has a stable ID, a name, a short description, a default color mode, an accent color, a font, spacing, a reading width and a radius from 0 to 24 pixels.

Light and dark palettes can independently override the background, panel surface, text, muted text, border and accent colors. Every color uses `#RRGGBB`. Omitted palette colors inherit the reader defaults; the selected accent applies unless the palette overrides it. Choose colors that remain readable in both modes.

A custom theme source looks like this:

```json
{
  "id": "team",
  "name": "Team handbook",
  "description": "Our documentation colors",
  "mode": "auto",
  "accent": "#315be8",
  "font": "system",
  "density": "comfortable",
  "width": "wide",
  "radius": 10,
  "light": {"background": "#ffffff", "surface": "#f5f7ff"},
  "dark": {"background": "#111827", "accent": "#a5b4fc"}
}
```

The CMS saves this file at `themes/team.json`. Applying it sets `themeId` and copies complete theme tokens into `docs.json`. Editing the active theme updates both files in one publication. Builtins cannot be changed or deleted, and an active custom theme must be replaced before deletion. A project supports at most 64 custom themes, each limited to 16 KiB.

Theme IDs match `[a-z0-9][a-z0-9_-]{0,63}`. Names allow 120 UTF-8 bytes and descriptions 512. Themes contain design tokens rather than arbitrary CSS, JavaScript or asset URLs. The compiler creates `_docs/appearance.css` and retains the reader's restrictive content security policy.

## Editing and publication policies

Choose the format used for new documents: Markdown, sanitized HTML, protobuf or OpenAPI. Automatic preview after saving a draft is optional. Changing this default does not convert existing source files.

**Publish valid changes automatically** publishes after a successful save. With this option off, saves remain drafts until publication. A manual conflict rebase remains a draft so the merged result can be reviewed before publishing.

**Push Git commits after publication** requires Git storage. A successful local publication remains available if the remote is offline. The CMS reports the pending push and keeps the commit for a later retry. Retrying synchronization does not need another source edit or duplicate commit.

Administrator access is required to change settings or themes. Source write grants still apply to `docs.json` and `themes/`. Updates carry the current project revision; a stale form must be reloaded before it can replace newer settings. A rejected compilation preserves the existing source and published site, and retains the proposed change as a recoverable draft.

## Interactive API requests

The API settings enable the request client and cURL controls, set a request timeout from 1000 to 30000 milliseconds, and limit response previews from 1024 to 1048576 bytes. Disabling the request client keeps the reference documentation readable and removes the interactive controls.

A request panel lets the reader choose a server, parameters, headers, authentication and body. Opening it loads the local contract. Only **Send** performs the target request. Additional headers are limited to 32 entries and cannot replace browser-controlled headers or collide with authentication or media controls.

The cURL preview redacts credentials and body data. **Copy cURL with current values** explicitly copies the entered values, including credentials; keep the resulting command private. The copied command targets a POSIX shell, such as Bash. It quotes shell metacharacters and sends a leading `@` as literal body content. Browser-managed cookies cannot be copied into cURL.

Requests run from the reader's browser and follow its network and CORS rules. Redirects are not followed. The response shows status, permitted headers and a bounded text preview; closing the panel cancels the request and clears its inputs. Credentials are not saved with the documentation or site settings.
