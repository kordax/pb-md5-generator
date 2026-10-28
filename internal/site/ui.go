package site

import (
	"html/template"
)

type pageView struct {
	Project       Manifest
	Page          Page
	Navigation    []navItem
	Breadcrumbs   []navItem
	GroupedNav    bool
	Headings      []heading
	Root          string
	Stylesheet    string
	Script        string
	Search        string
	NeedsTitle    bool
	ModelURL      string
	APIStylesheet string
	APIScript     string
	Interactive   bool
	AppearanceCSS string
	ThemeMode     string
	Logo          string
	Favicon       string
	ShowTOC       bool
	Previous      *navItem
	Next          *navItem
	ShowCurl      bool
	TimeoutMs     int
	ResponseBytes int
	DocsURL       string
	APIURL        string
	StudioURL     string
}

type navItem struct {
	Title       string
	URL         string
	Current     bool
	Group       bool
	ID          string
	Description string
	Open        bool
	Children    []navItem
}

var pageStart = template.Must(template.New("start").Parse(`{{define "navigation-items"}}{{range .}}<li>{{if .Group}}<details class="nav-group" id="_docs-nav-{{.ID}}"{{if .Open}} open{{end}}><summary>{{.Title}}</summary>{{if .Description}}<p class="nav-group-description">{{.Description}}</p>{{end}}<ul>{{template "navigation-items" .Children}}</ul></details>{{else}}<a href="{{.URL}}"{{if .Current}} aria-current="page"{{end}}>{{.Title}}</a>{{end}}</li>{{end}}{{end}}<!doctype html>
<html lang="en"{{if ne .ThemeMode "auto"}} data-theme="{{.ThemeMode}}"{{end}}>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' https:; object-src 'none'; base-uri 'none'; form-action 'none';{{if .Interactive}} connect-src 'self' http: https:;{{end}}">
<meta name="description" content="{{.Project.Description}}">
<title>{{.Page.Title}} · {{.Project.Title}}</title>
<link rel="stylesheet" href="{{.Stylesheet}}">
<link rel="stylesheet" href="{{.AppearanceCSS}}">
{{if .Favicon}}<link rel="icon" href="{{.Favicon}}">{{end}}
<link rel="alternate" type="application/vnd.pb-docs.page+json" href="{{.ModelURL}}">
{{if .APIStylesheet}}<link rel="stylesheet" href="{{.APIStylesheet}}">{{end}}
<script src="{{.Script}}" data-search="{{.Search}}" defer></script>
{{if .APIScript}}<script src="{{.APIScript}}" data-api-show-curl="{{.ShowCurl}}" data-api-timeout="{{.TimeoutMs}}" data-api-response-bytes="{{.ResponseBytes}}" defer></script>{{end}}
</head>
<body{{if .APIStylesheet}} class="api-page"{{end}}>
<a class="skip-link" href="#_docs-main">Skip to content</a>
<header class="topbar">
<a class="brand" href="{{.Root}}">{{if .Logo}}<img class="brand-logo" src="{{.Logo}}" alt="">{{else}}<svg class="brand-mark" aria-hidden="true" width="21" height="21" viewBox="0 0 21 21" fill="currentColor"><path d="M0 0h8v8H0zM11 11h8v8h-8z"/><path d="M11 0h8v8h-8z" opacity=".45"/><path d="M0 11h8v8H0z" opacity=".7"/></svg>{{end}}{{.Project.Title}}</a>
<nav class="workspace-nav" aria-label="Workspace">{{if .DocsURL}}<a href="{{.DocsURL}}"{{if not .APIStylesheet}} aria-current="true"{{end}}>Documentation</a>{{end}}{{if .APIURL}}<a href="{{.APIURL}}"{{if .APIStylesheet}} aria-current="true"{{end}}>API reference</a>{{end}}{{if .StudioURL}}<a href="{{.StudioURL}}">Studio</a>{{end}}</nav>
<div id="_docs-search-box" class="search-box" hidden>
<label for="_docs-search">Search documentation</label>
<input id="_docs-search" type="search" placeholder="Search documentation…" autocomplete="off" maxlength="128" aria-controls="_docs-results">
<p id="_docs-search-status" class="search-status" role="status"></p>
<ul id="_docs-results" class="search-results"></ul>
</div>
<div class="toolbar"><button id="_docs-theme" type="button" hidden>Theme</button></div>
</header>
<div class="layout">
<aside class="sidebar">
<div class="sidebar-project">{{.Project.Title}}</div>
<details class="mobile-navigation" open><summary>Navigation</summary>
<nav aria-label="Documentation"><p class="nav-caption">CONTENTS</p><ul class="navigation">
{{if .GroupedNav}}{{template "navigation-items" .Navigation}}{{else}}{{range .Navigation}}<li><a href="{{.URL}}"{{if .Current}} aria-current="page"{{end}}>{{.Title}}</a></li>{{end}}{{end}}
</ul></nav>{{if .APIStylesheet}}<nav class="endpoint-navigation" aria-label="API sections"><p class="nav-caption">API REFERENCE</p><ul class="navigation">{{range .Headings}}{{if or (eq .Level 2) (eq .Level 3)}}<li class="endpoint-level-{{.Level}}"><a href="#{{.ID}}">{{.Title}}</a></li>{{end}}{{end}}</ul></nav>{{end}}</details>
</aside>
<main id="_docs-main" tabindex="-1">{{if .GroupedNav}}<nav class="breadcrumb" aria-label="Breadcrumb"><ol><li>{{.Project.Title}}</li>{{range .Breadcrumbs}}<li{{if .Current}} aria-current="page"{{end}}>{{.Title}}</li>{{end}}</ol></nav>{{else}}<div class="breadcrumb">{{.Project.Title}} <span aria-hidden="true">/</span> {{.Page.Title}}</div>{{end}}
{{if .ShowTOC}}<details class="mobile-toc"><summary>On this page</summary><nav aria-label="On this page"><ul>{{range .Headings}}<li class="toc-level-{{.Level}}"><a href="#{{.ID}}">{{.Title}}</a></li>{{end}}</ul></nav></details>{{end}}
<article>{{if .NeedsTitle}}<h1>{{.Page.Title}}</h1>{{end}}
`))

var pageEnd = template.Must(template.New("end").Parse(`
</article>{{if or .Previous .Next}}<nav class="page-sequence" aria-label="Adjacent pages">{{if .Previous}}<a class="previous" rel="prev" href="{{.Previous.URL}}">← {{.Previous.Title}}</a>{{end}}{{if .Next}}<a class="next" rel="next" href="{{.Next.URL}}">{{.Next.Title}} →</a>{{end}}</nav>{{end}}<footer class="page-footer">{{if .Project.Footer}}{{.Project.Footer}}{{else}}{{.Project.Title}}{{end}}</footer></main>
{{if .ShowTOC}}<aside class="toc"><nav aria-label="On this page"><p class="nav-caption">ON THIS PAGE</p><ul>
{{range .Headings}}<li class="toc-level-{{.Level}}"><a href="#{{.ID}}">{{.Title}}</a></li>{{end}}
</ul></nav></aside>{{end}}
</div>
</body>
</html>
`))

func renderPage(manifest Manifest, current int, content pageContent) ([]byte, error) {
	page := manifest.Pages[current]
	appearance := manifest.Appearance()
	home := manifest.Pages[0].Path
	for _, candidate := range manifest.Pages {
		if candidate.Path == "index.html" {
			home = candidate.Path
			break
		}
	}
	view := pageView{
		Project:       manifest,
		Page:          page,
		Headings:      content.Headings,
		Root:          relativeURL(page.Path, home),
		Stylesheet:    relativeURL(page.Path, "_docs/site.css"),
		Script:        relativeURL(page.Path, "_docs/site.js"),
		Search:        relativeURL(page.Path, "_docs/search.json"),
		NeedsTitle:    true,
		ModelURL:      relativeURL(page.Path, pageDataPath(page)),
		Interactive:   content.Interactive,
		AppearanceCSS: relativeURL(page.Path, "_docs/appearance.css"),
		ThemeMode:     appearance.Theme.Mode,
		ShowTOC:       appearance.Navigation.ShowTOC,
		ShowCurl:      appearance.API.ShowCurl,
		TimeoutMs:     appearance.API.RequestTimeoutMs,
		ResponseBytes: appearance.API.MaxResponseBytes,
		StudioURL:     manifest.ReaderStudioURL,
	}
	for _, candidate := range manifest.Pages {
		api := candidate.Kind == "openapi" || candidate.Kind == "protobuf"
		if !api && view.DocsURL == "" {
			view.DocsURL = relativeURL(page.Path, candidate.Path)
		}
		if api && view.APIURL == "" {
			view.APIURL = relativeURL(page.Path, candidate.Path)
		}
	}
	if manifest.Logo != "" {
		view.Logo = relativeURL(page.Path, "assets/"+manifest.Logo)
	}
	if manifest.Favicon != "" {
		view.Favicon = relativeURL(page.Path, "assets/"+manifest.Favicon)
	}
	if appearance.Navigation.ShowPreviousNext && appearance.Navigation.Tree == nil {
		if current > 0 {
			previous := manifest.Pages[current-1]
			view.Previous = &navItem{Title: previous.Title, URL: relativeURL(page.Path, previous.Path)}
		}
		if current+1 < len(manifest.Pages) {
			next := manifest.Pages[current+1]
			view.Next = &navItem{Title: next.Title, URL: relativeURL(page.Path, next.Path)}
		}
	}
	if content.APIData != "" {
		view.APIStylesheet = relativeURL(page.Path, "_docs/api.css")
	}
	if content.Interactive {
		view.APIScript = relativeURL(page.Path, "_docs/api-client.js")
	}
	for _, heading := range content.Headings {
		if heading.Level == 1 {
			view.NeedsTitle = false
		}
	}
	if appearance.Navigation.Tree == nil {
		for i, item := range manifest.Pages {
			view.Navigation = append(view.Navigation, navItem{Title: item.Title, URL: relativeURL(page.Path, item.Path), Current: i == current})
		}
	} else {
		navigation, err := ResolveNavigation(manifest, page.ID)
		if err != nil {
			return nil, err
		}
		view.GroupedNav = true
		view.Navigation = renderNavigationItems(navigation.Items, page.Path)
		view.Breadcrumbs = renderNavigationItems(navigation.Breadcrumbs, page.Path)
		if appearance.Navigation.ShowPreviousNext {
			for index, item := range navigation.Pages {
				if item.PageID != page.ID {
					continue
				}
				if index > 0 {
					previous := navigation.Pages[index-1]
					view.Previous = &navItem{Title: previous.Title, URL: relativeURL(page.Path, previous.Path)}
				}
				if index+1 < len(navigation.Pages) {
					next := navigation.Pages[index+1]
					view.Next = &navItem{Title: next.Title, URL: relativeURL(page.Path, next.Path)}
				}
				break
			}
		}
	}
	result := boundedBuffer{limit: maxRenderedBytes}
	if err := pageStart.Execute(&result, view); err != nil {
		return nil, err
	}
	// HTML comes from safe Markdown and API renderers. Keep it outside
	// html/template so untrusted manifest values always use contextual escaping.
	if _, err := result.WriteString(content.HTML); err != nil {
		return nil, err
	}
	if err := pageEnd.Execute(&result, view); err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}

func renderNavigationItems(items []NavigationItem, currentPath string) []navItem {
	result := make([]navItem, 0, len(items))
	for _, item := range items {
		view := navItem{Title: item.Title, Current: item.Current, Group: item.Kind == "group", ID: item.ID, Description: item.Description, Open: !item.Collapsed || item.Active}
		if view.Group {
			view.Children = renderNavigationItems(item.Children, currentPath)
		} else {
			view.URL = relativeURL(currentPath, item.Path)
		}
		result = append(result, view)
	}
	return result
}

const siteCSS = `:root{color-scheme:light dark;--bg:#fff;--panel:#fafafa;--text:#18181b;--muted:#71717a;--line:#e4e4e7;--accent:#6557d2;--code:#f4f4f5;--space-1:4px;--space-2:8px;--space-3:12px;--space-4:16px;--space-6:24px;--space-8:32px;--space-12:48px;--reader-measure:42rem;--header-height:68px;--copy-target:40px}
@media(prefers-color-scheme:dark){:root{--bg:#191a1f;--panel:#222329;--text:#e4e4e7;--muted:#a1a1aa;--line:#37383f;--accent:#a99bec;--code:#292a32}}
:root[data-theme=light]{color-scheme:light;--bg:#fff;--panel:#fafafa;--text:#18181b;--muted:#71717a;--line:#e4e4e7;--accent:#6557d2;--code:#f4f4f5}
:root[data-theme=dark]{color-scheme:dark;--bg:#191a1f;--panel:#222329;--text:#e4e4e7;--muted:#a1a1aa;--line:#37383f;--accent:#a99bec;--code:#292a32}
*{box-sizing:border-box}::selection{background:var(--accent);color:var(--bg)}input,textarea{caret-color:var(--accent)}input::placeholder,textarea::placeholder{color:var(--muted);opacity:1}body{margin:0;background:var(--bg);color:var(--text);font:15px/1.75 "DM Sans",ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}a{color:var(--accent);text-underline-offset:.2em}button,input,textarea,select{font:inherit}button,summary{cursor:pointer}button{border:1px solid var(--line);border-radius:8px;background:var(--panel);color:var(--text);padding:8px 12px;min-height:40px;line-height:1.4}button:hover{border-color:var(--accent);background:var(--code)}button:active{background:var(--code)}button:disabled{cursor:default;opacity:.55}:focus-visible{outline:3px solid var(--accent);outline-offset:3px}[hidden]{display:none!important}.skip-link{position:fixed;top:-100px;left:20px;z-index:10;background:var(--bg);padding:10px}.skip-link:focus{top:8px}.topbar{height:var(--header-height);position:sticky;top:0;z-index:2;display:flex;align-items:center;justify-content:space-between;padding:0 var(--space-6);border-bottom:1px solid var(--line);background:var(--bg)}.brand{display:flex;align-items:center;gap:var(--space-3);min-width:0;overflow-wrap:anywhere;font-weight:700;font-size:18px;color:var(--text);text-decoration:none}.brand-mark{display:grid;place-items:center;width:32px;height:32px;flex:none;border-radius:8px;background:var(--accent);color:var(--bg);font-size:20px}.layout{max-width:1600px;margin:auto;display:grid;grid-template-columns:224px minmax(0,1fr) 200px;min-height:calc(100vh - var(--header-height))}.sidebar{padding:var(--space-8) var(--space-6);border-right:1px solid var(--line)}.sidebar,.toc{position:sticky;top:var(--header-height);align-self:start;height:calc(100vh - var(--header-height));overflow:auto}.nav-caption{font-size:12px;line-height:1.5;font-weight:650;letter-spacing:.04em;color:var(--muted);margin:var(--space-6) 0 var(--space-2)}.navigation,.toc ul,.search-results{list-style:none;padding:0;margin:0}.navigation a{display:block;padding:8px 12px;margin:4px 0;border-radius:8px;color:var(--muted);font-size:14px;line-height:1.5;text-decoration:none}.navigation a[aria-current=page]{color:var(--accent);background:var(--code);font-weight:650}.navigation a:hover{background:var(--code)}.mobile-navigation>summary{display:none}.mobile-navigation:not([open])>summary{display:block}.mobile-toc{display:none}.toolbar{flex:none;margin-inline-start:16px}.sidebar-foot{border-top:1px solid var(--line);margin-top:35px;padding-top:18px;color:var(--muted);font-size:12px}.search-box label{font-size:12px;color:var(--muted)}input[type=search]{display:block;margin:8px 0 0;width:100%;min-width:0;min-height:40px;padding:8px 12px;border:1px solid var(--line);border-radius:8px;background:var(--bg);color:var(--text);font-size:13px}.search-status{font-size:12px;line-height:1.5;color:var(--muted);margin:8px 0}.search-status:empty{display:none}.search-results a{display:block;padding:12px 0;font-size:13px;font-weight:600;text-decoration:none;overflow-wrap:anywhere}.search-results li+li{border-top:1px solid var(--line)}.search-results small{display:block;color:var(--muted);font-weight:400}.breadcrumb{font-size:12px;line-height:1.5;color:var(--muted);margin-bottom:32px}.breadcrumb span{padding:0 8px}main{padding:32px 48px;min-width:0}.layout main>article,.layout main>.breadcrumb,.layout main>.page-sequence,.layout main>.page-footer{width:100%;max-width:min(var(--docs-article-width,900px),var(--reader-measure));margin-inline:auto}.api-page main>article,.api-page main>.breadcrumb,.api-page main>.page-sequence,.api-page main>.page-footer{max-width:var(--docs-article-width,900px)}article{min-width:0;margin:auto}h1,h2,h3,h4,h5,h6{line-height:1.3;letter-spacing:-.02em;scroll-margin-top:96px;text-wrap:balance;overflow-wrap:anywhere}h1{font-size:36px;margin:0 0 24px}h2{font-size:26px;margin:48px 0 16px}h3{font-size:21px;margin:32px 0 12px}h4,h5,h6{margin:24px 0 8px}p,ul,ol{margin:16px 0}p{ text-wrap:pretty}li+li{margin-top:4px}.navigation li+li,.toc li+li,.mobile-toc li+li{margin-top:0}article> :first-child{margin-top:0}code,pre{font: .88em/1.65 "IBM Plex Mono",ui-monospace,SFMono-Regular,Consolas,monospace}code{background:var(--code);padding:.16em .34em;border-radius:4px}pre{position:relative;padding:24px;border:1px solid var(--line);border-radius:8px;background:var(--panel);overflow:auto}pre code{padding:0;background:none;border-radius:0}pre:has(.copy-code){padding-top:calc(var(--copy-target) + 24px)}.copy-code{position:absolute;right:12px;top:12px;min-width:var(--copy-target);min-height:var(--copy-target);padding:4px 8px;font-size:12px}blockquote{margin:24px 0;padding:4px 24px;border-left:1px solid var(--accent);background:var(--panel);color:var(--muted)}table{display:block;overflow:auto;width:100%;border-collapse:collapse;font-size:14px;margin:24px 0}th,td{text-align:left;padding:12px 16px;border:1px solid var(--line);vertical-align:top}th{background:var(--panel);font-weight:600}img{max-width:100%;height:auto}hr{border:0;border-top:1px solid var(--line);margin:36px 0}.toc{padding:32px 24px 32px 0}.toc .nav-caption{margin-top:0}.toc a{display:block;padding:4px 0;font-size:13px;line-height:1.5;text-decoration:none;color:var(--muted);overflow-wrap:anywhere}.toc a:hover{color:var(--accent)}.toc-level-3,.toc-level-4,.toc-level-5,.toc-level-6{margin-left:12px}.page-footer{margin-top:48px;padding-top:24px;border-top:1px solid var(--line);color:var(--muted);font-size:12px}
.navigation ul{list-style:none;margin:0 0 0 12px;padding:0 0 0 8px;border-inline-start:1px solid var(--line)}.navigation .nav-group>summary{display:list-item;padding:8px 12px;color:var(--text);font-size:14px;font-weight:650;overflow-wrap:anywhere}.nav-group-description{margin:4px 12px 10px;font-size:12px;color:var(--muted);overflow-wrap:anywhere}.breadcrumb ol{display:flex;flex-wrap:wrap;gap:0;list-style:none;margin:0;padding:0}.breadcrumb li+li::before{content:"/";padding:0 10px}.navigation a{overflow-wrap:anywhere}
.docs-callout{--callout-accent:var(--accent);margin:24px 0;padding:16px 24px;border:1px solid var(--line);border-inline-start:1px solid var(--callout-accent);border-radius:8px;background:var(--panel);overflow-wrap:anywhere}.docs-callout-tip{--callout-accent:#16a34a}.docs-callout-info{--callout-accent:#0891b2}.docs-callout-warning{--callout-accent:#d97706}.docs-callout-danger{--callout-accent:#dc2626}.docs-callout-title{margin:0 0 8px;font-weight:700}.docs-callout>:last-child{margin-bottom:0}.docs-tabs{min-width:0;margin:24px 0}.docs-tab-links{display:flex;flex-wrap:wrap;gap:4px;margin:0;padding:8px;border:1px solid var(--line);border-radius:10px 10px 0 0;background:var(--panel)}.docs-tab-links>a{display:block;max-width:100%;padding:6px 12px;border:1px solid transparent;border-radius:7px;color:var(--muted);font-weight:600;font-size:14px;text-decoration:none;overflow-wrap:anywhere}.docs-tab-links>a:hover,.docs-tab-links>a[aria-selected=true]{color:var(--text);background:var(--bg);border-color:var(--line)}.docs-tab-panel{padding:24px;border:1px solid var(--line);border-top:0}.docs-tab-panel:last-child{border-radius:0 0 10px 10px}.docs-tab-panel>:first-child{margin-top:0}.docs-tab-panel>:last-child{margin-bottom:0}.docs-tab-title{font-weight:700}.docs-tabs-ready>.docs-tab-panel>.docs-tab-title{display:none}.docs-include{min-width:0}
@media print{.docs-tab-links{display:none}.docs-tabs-ready>.docs-tab-panel[hidden]{display:block!important}.docs-tabs-ready>.docs-tab-panel>.docs-tab-title{display:block}.docs-tab-panel{border:0;padding:8px 0}.docs-tab-title,.docs-callout-title{break-after:avoid}}
@media(max-width:1150px){.layout{grid-template-columns:224px minmax(0,1fr)}.toc{display:none}.mobile-toc{display:block;border-block:1px solid var(--line);margin:0 auto 32px;max-width:var(--reader-measure);font-size:14px}.mobile-toc>summary{padding:12px 0;font-weight:600}.mobile-toc ul{list-style:none;margin:0;padding:0 0 12px}.mobile-toc a{display:block;min-height:40px;padding:8px 0;color:var(--muted);text-decoration:none;overflow-wrap:anywhere}.api-page .mobile-toc{max-width:var(--docs-article-width,900px)}}@media(max-width:760px){:root{--header-height:64px}.topbar{padding:0 20px}.brand{font-size:15px}.brand-logo{max-width:120px}.layout{display:block}.sidebar{height:auto;position:static;border-right:0;border-bottom:1px solid var(--line);padding:16px 20px}.search-box{margin-bottom:12px}.mobile-navigation>summary,.mobile-navigation:not([open])>summary{display:list-item;padding:8px 0;font-size:14px;font-weight:600}.nav-caption{margin-top:16px}.sidebar-foot{display:none}main{padding:24px 20px}.breadcrumb{margin-bottom:24px}h1{font-size:30px}h2{font-size:24px;margin-top:40px}.mobile-toc{margin-bottom:24px}.navigation{max-height:50vh;overflow:auto}.navigation a{min-height:40px}.docs-tab-panel{padding:16px}pre{padding:16px}.page-sequence{flex-wrap:wrap}}
@media(max-width:760px),(pointer:coarse){:root{--copy-target:44px}}
@media(min-width:761px) and (max-width:1300px){main{padding-inline:32px}}@media(prefers-reduced-motion:no-preference){html{scroll-behavior:smooth}}@media print{.topbar,.sidebar,.toc,.mobile-toc,.copy-code,.skip-link,.breadcrumb{display:none}.layout{display:block}main{padding:0}pre,table{break-inside:avoid}}
@font-face{font-family:"DM Sans";font-style:normal;font-weight:400 700;font-display:swap;src:url("fonts/dm-sans-latin.woff2") format("woff2");unicode-range:U+0000-00FF,U+0131,U+0152-0153,U+02BB-02BC,U+02C6,U+02DA,U+02DC,U+0304,U+0308,U+0329,U+2000-206F,U+20AC,U+2122,U+2191,U+2193,U+2212,U+2215,U+FEFF,U+FFFD}
@font-face{font-family:"DM Sans";font-style:normal;font-weight:400 700;font-display:swap;src:url("fonts/dm-sans-latin-ext.woff2") format("woff2");unicode-range:U+0100-02BA,U+02BD-02C5,U+02C7-02CC,U+02CE-02D7,U+02DD-02FF,U+0300-0303,U+0305-0307,U+0309-0328,U+032A-036F,U+1D00-1DBF,U+1E00-1E9F,U+1EF2-1EFF,U+2020,U+20A0-20AB,U+20AD-20C0,U+2113,U+2C60-2C7F,U+A720-A7FF}
@font-face{font-family:"IBM Plex Mono";font-style:normal;font-weight:400;font-display:swap;src:url("fonts/ibm-plex-mono-latin.woff2") format("woff2");unicode-range:U+0000-00FF,U+0131,U+0152-0153,U+02BB-02BC,U+02C6,U+02DA,U+02DC,U+0304,U+0308,U+0329,U+2000-206F,U+20AC,U+2122,U+2191,U+2193,U+2212,U+2215,U+FEFF,U+FFFD}
@font-face{font-family:"IBM Plex Mono";font-style:normal;font-weight:400;font-display:swap;src:url("fonts/ibm-plex-mono-cyrillic.woff2") format("woff2");unicode-range:U+0301,U+0400-045F,U+0490-0491,U+04B0-04B1,U+2116}
@font-face{font-family:"IBM Plex Mono";font-style:normal;font-weight:500;font-display:swap;src:url("fonts/ibm-plex-mono-medium-latin.woff2") format("woff2");unicode-range:U+0000-00FF,U+0131,U+0152-0153,U+02BB-02BC,U+02C6,U+02DA,U+02DC,U+0304,U+0308,U+0329,U+2000-206F,U+20AC,U+2122,U+2191,U+2193,U+2212,U+2215,U+FEFF,U+FFFD}
@font-face{font-family:"IBM Plex Mono";font-style:normal;font-weight:500;font-display:swap;src:url("fonts/ibm-plex-mono-medium-cyrillic.woff2") format("woff2");unicode-range:U+0301,U+0400-045F,U+0490-0491,U+04B0-04B1,U+2116}
*{scrollbar-width:thin;scrollbar-color:var(--line) transparent}body{-webkit-font-smoothing:antialiased}button,input,textarea,select{border-radius:5px}.topbar{gap:28px;padding-inline:28px}.brand{font-size:19px;font-weight:650;letter-spacing:-.035em}.brand-mark{width:21px;height:21px;display:block;color:var(--accent);background:none;border-radius:0}.brand-mark i{background:var(--accent);height:8px}.brand-mark i:nth-child(2){opacity:.45}.brand-mark i:nth-child(3){opacity:.7}.workspace-nav{display:flex;align-self:stretch;gap:28px;flex:none}.workspace-nav a{display:flex;align-items:center;position:relative;color:var(--muted);font-size:14px;text-decoration:none;white-space:nowrap}.workspace-nav a:hover,.workspace-nav a[aria-current]{color:var(--text)}.workspace-nav a[aria-current]{font-weight:600}.workspace-nav a[aria-current]::after{content:"";height:2px;position:absolute;inset:auto 0 0;background:var(--accent)}.toolbar{margin:0}.toolbar button{font-size:12px;min-height:36px;padding:6px 10px;background:transparent}.search-box{width:230px;min-width:150px;margin-inline-start:auto;position:relative}.search-box label{position:absolute;width:1px;height:1px;padding:0;overflow:hidden;clip-path:inset(50%);white-space:nowrap}.search-box input{height:36px;min-height:36px;font-size:12px;margin:0;background:var(--panel);border-radius:5px}.search-box:has(.search-results:not(:empty),.search-status:not(:empty)){z-index:5}.search-status:not(:empty),.search-results:not(:empty){position:absolute;inset-inline:0;background:var(--bg);border:1px solid var(--line);padding:12px;margin:0;box-shadow:0 8px 24px #18181b12}.search-status:not(:empty){top:40px;min-width:230px}.search-results:not(:empty){top:82px;min-width:280px;max-height:65vh;overflow:auto}.layout{max-width:1800px}.sidebar{padding:26px 18px}.sidebar-project{font-size:13px;font-weight:650;padding:0 10px 18px;overflow-wrap:anywhere}.nav-caption{font-size:10px;letter-spacing:.1em;font-weight:650;margin:18px 10px 8px}.navigation a{padding:7px 10px;margin:2px 0;border-radius:5px;font-size:13px}.navigation a[aria-current=page]{background:color-mix(in srgb,var(--accent) 10%,transparent);font-weight:550}.navigation .nav-group>summary{font-size:12px;padding:9px 10px;font-weight:650}.navigation ul{margin-left:10px;padding-left:8px}.endpoint-navigation{margin-top:24px;border-top:1px solid var(--line);padding-top:6px}.endpoint-level-2 a{font-weight:600;color:var(--text)}.endpoint-level-3 a{font-size:12px;padding-left:18px}.breadcrumb{font-size:12px;margin-bottom:28px}main{padding:34px 40px 48px}h1{font-size:34px;letter-spacing:-.035em;font-weight:650;line-height:1.2;margin-bottom:20px}h2{font-size:25px;font-weight:600;letter-spacing:-.025em}h3{font-size:20px;font-weight:600}article>h1+p{font-size:17px;line-height:1.75;color:var(--muted)}pre{border-radius:5px;font-size:13px;padding:20px}.toc{padding:36px 20px 32px 8px}.toc .nav-caption{margin:0 0 14px;font-size:11px;letter-spacing:0;text-transform:none}.toc a{font-size:12px;padding:6px 0}.page-sequence{padding-top:24px;border-top:1px solid var(--line)}.page-sequence a{font-size:13px;text-decoration:none}.page-footer{margin-top:32px;padding-top:20px}.docs-callout{border-radius:5px}.docs-tab-links{border-radius:5px 5px 0 0;background:transparent;padding:0;gap:20px;border-inline:0;border-top:0}.docs-tab-links>a{padding:10px 0;border:0;border-bottom:2px solid transparent;border-radius:0;font-weight:500}.docs-tab-links>a:hover,.docs-tab-links>a[aria-selected=true]{background:none;border-color:var(--accent);color:var(--accent)}.docs-tab-panel{border-inline:0;padding:20px 0}
@media(max-width:1100px){.topbar{gap:20px}.workspace-nav{gap:20px}.search-box{width:190px}.brand{font-size:16px}.toolbar button{font-size:11px}.topbar .brand-logo{max-width:110px}}@media(max-width:900px){.search-box{width:160px;min-width:120px}.workspace-nav{gap:14px}.workspace-nav a{font-size:12px}.topbar{gap:16px;padding-inline:20px}.toolbar button{max-width:72px}.brand{font-size:15px}}@media(max-width:760px){.topbar{height:auto;min-height:68px;position:relative;flex-wrap:wrap;padding:16px 20px;gap:14px 20px}.brand{max-width:calc(100% - 92px);font-size:17px}.toolbar{margin-inline-start:auto;order:1}.workspace-nav{order:2;width:100%;min-height:34px;gap:24px}.workspace-nav a{font-size:13px;padding-bottom:10px}.search-box{order:3;width:100%;margin:0}.search-box input{font-size:13px}.search-status:not(:empty),.search-results:not(:empty){min-width:0}.sidebar{padding:10px 20px}.sidebar-project{display:none}.mobile-navigation>summary{padding:10px 0}.sidebar .nav-caption{margin-left:10px}.endpoint-navigation{margin-top:16px}main{padding:26px 20px 40px}h1{font-size:30px}.mobile-toc{font-size:13px}.topbar:has(.brand-logo){align-items:center}}
`

const siteJS = `(() => {
  "use strict";
  const script = document.currentScript;
  const theme = document.getElementById("_docs-theme");
  const root = document.documentElement;
  function applyTheme(value) {
    if (value === "light" || value === "dark") root.dataset.theme = value;
    const dark = root.dataset.theme ? root.dataset.theme === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;
    theme.textContent = dark ? "Light theme" : "Dark theme";
  }
  let saved;
  try { saved = localStorage.getItem("pb-docs-theme"); } catch (_) {}
  applyTheme(saved);
  theme.hidden = false;
  theme.addEventListener("click", () => {
    const dark = root.dataset.theme ? root.dataset.theme === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;
    const next = dark ? "light" : "dark";
    applyTheme(next);
    try { localStorage.setItem("pb-docs-theme", next); } catch (_) {}
  });
  if (navigator.clipboard && window.isSecureContext) {
    document.querySelectorAll("pre > code").forEach(code => {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "copy-code";
      button.textContent = "Copy";
      button.addEventListener("click", async () => {
        try { await navigator.clipboard.writeText(code.textContent); button.textContent = "Copied"; }
        catch (_) { button.textContent = "Copy unavailable"; }
      });
      code.parentElement.prepend(button);
    });
  }
  const navigation = document.querySelector(".mobile-navigation");
  if (navigation) {
    const mobile = matchMedia("(max-width: 760px)");
    let mobileOpen = false;
    const syncNavigation = () => { navigation.open = !mobile.matches || mobileOpen; };
    navigation.addEventListener("toggle", () => { if (mobile.matches) mobileOpen = navigation.open; });
    mobile.addEventListener("change", syncNavigation);
    syncNavigation();
  }
  function fragmentTarget(fragment) {
    if (!fragment || fragment[0] !== "#") return null;
    try { return document.getElementById(decodeURIComponent(fragment.slice(1))); }
    catch (_) { return null; }
  }
  const tabGroups = [];
  for (const group of document.querySelectorAll("[data-docs-tabs]")) {
    const lists = Array.from(group.children).filter(child => child.hasAttribute("data-docs-tablist"));
    const panels = Array.from(group.children).filter(child => child.hasAttribute("data-docs-tabpanel"));
    if (lists.length !== 1 || !panels.length) continue;
    const list = lists[0];
    const tabs = Array.from(list.children);
    if (tabs.length !== panels.length || tabs.some((tab, index) =>
      tab.tagName.toLowerCase() !== "a" || !tab.id || !panels[index].id ||
      tab.getAttribute("data-docs-tab") !== panels[index].id ||
      tab.getAttribute("href") !== "#" + panels[index].id ||
      document.getElementById(tab.id) !== tab || document.getElementById(panels[index].id) !== panels[index]
    ) || new Set(tabs.map(tab => tab.id).concat(panels.map(panel => panel.id))).size !== tabs.length * 2) continue;
    let selected = -1;
    const select = (index, focus) => {
      const changed = selected !== index;
      selected = index;
      tabs.forEach((tab, i) => { tab.setAttribute("aria-selected", String(i === index)); tab.tabIndex = i === index ? 0 : -1; });
      panels.forEach((panel, i) => { panel.hidden = i !== index; });
      if (focus) tabs[index].focus();
      return changed;
    };
    list.setAttribute("role", "tablist");
    tabs.forEach((tab, index) => {
      tab.setAttribute("role", "tab");
      tab.setAttribute("aria-controls", panels[index].id);
      panels[index].setAttribute("role", "tabpanel");
      panels[index].setAttribute("aria-labelledby", tab.id);
      panels[index].tabIndex = 0;
      tab.addEventListener("click", event => {
        if (event.ctrlKey || event.metaKey || event.altKey || event.shiftKey || (event.button !== undefined && event.button !== 0)) return;
        event.preventDefault(); select(index, true);
      });
      tab.addEventListener("keydown", event => {
        if (event.ctrlKey || event.metaKey || event.altKey || event.shiftKey) return;
        const next = event.key === "ArrowRight" ? (index + 1) % tabs.length :
          event.key === "ArrowLeft" ? (index + tabs.length - 1) % tabs.length :
          event.key === "Home" ? 0 : event.key === "End" ? tabs.length - 1 : -1;
        if (next !== -1) { event.preventDefault(); select(next, true); }
      });
    });
    group.classList.add("docs-tabs-ready");
    tabGroups.push({ tabs, panels, select });
    const target = fragmentTarget(window.location.hash);
    const initial = panels.findIndex(panel => panel === target || panel.contains(target));
    select(initial < 0 ? 0 : initial, false);
  }
  if (tabGroups.length) {
    const reveal = fragment => {
      const target = fragmentTarget(fragment);
      if (!target) return;
      let changed = false;
      for (const group of tabGroups) {
        let index = group.panels.findIndex(panel => panel === target || panel.contains(target));
        if (index < 0) index = group.tabs.indexOf(target);
        if (index >= 0) changed = group.select(index, false) || changed;
      }
      if (changed) target.scrollIntoView({ block: "start" });
    };
    window.addEventListener("hashchange", () => reveal(window.location.hash));
    document.addEventListener("click", event => {
      if (event.defaultPrevented || event.ctrlKey || event.metaKey || event.altKey || event.shiftKey || (event.button !== undefined && event.button !== 0)) return;
      const link = event.target.closest("a[href^='#']");
      if (link) reveal(link.getAttribute("href"));
    });
    reveal(window.location.hash);
  }
  const input = document.getElementById("_docs-search");
  const results = document.getElementById("_docs-results");
  const status = document.getElementById("_docs-search-status");
  const indexURL = new URL(script.dataset.search, document.baseURI);
  const siteURL = new URL("../", indexURL);
  document.getElementById("_docs-search-box").hidden = false;
  let indexPromise;
  function loadIndex() {
    if (!indexPromise) indexPromise = fetch(indexURL).then(response => {
      if (!response.ok) throw new Error("Search unavailable");
      return response.json();
    }).then(entries => entries.map(entry => ({...entry, terms: (entry.title + " " + entry.text).toLocaleLowerCase()}))).catch(error => { indexPromise = null; throw error; });
    return indexPromise;
  }
  let timer;
  let generation = 0;
  input.addEventListener("input", () => {
    clearTimeout(timer);
    const run = ++generation;
    results.replaceChildren();
    status.textContent = "";
    const query = input.value.trim().toLocaleLowerCase();
    if (!query) return;
    timer = setTimeout(async () => {
      status.textContent = "Searching…";
      try {
        const entries = await loadIndex();
        if (run !== generation) return;
        const words = query.split(/\s+/).slice(0, 8);
        const matches = entries.filter(entry => words.every(word => entry.terms.includes(word)));
        for (const entry of matches.slice(0, 10)) {
          const li = document.createElement("li");
          const link = document.createElement("a");
          link.href = new URL(entry.url, siteURL).href;
          link.textContent = entry.title;
          const snippet = document.createElement("small");
          const offset = Math.max(0, entry.text.toLocaleLowerCase().indexOf(words[0]) - 32);
          snippet.textContent = entry.text.slice(offset, offset + 140);
          link.append(snippet); li.append(link); results.append(li);
        }
        status.textContent = matches.length ? matches.length + " page(s) found" + (matches.length > 10 ? "; showing first 10" : "") : "No matching pages";
      } catch (_) { if (run === generation) status.textContent = "Search unavailable. Serve this directory over HTTP to enable search."; }
    }, 120);
  });
  input.addEventListener("keydown", event => {
    if (event.key === "Escape") { input.value = ""; input.dispatchEvent(new Event("input")); }
    if (event.key === "ArrowDown") { const first = results.querySelector("a"); if (first) { event.preventDefault(); first.focus(); } }
  });
})();
`
