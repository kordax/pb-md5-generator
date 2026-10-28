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
<a class="brand" href="{{.Root}}">{{if .Logo}}<img class="brand-logo" src="{{.Logo}}" alt="">{{else}}<span class="brand-mark" aria-hidden="true">D</span>{{end}}{{.Project.Title}}</a>
<div class="toolbar"><button id="_docs-theme" type="button" hidden>Theme</button></div>
</header>
<div class="layout">
<aside class="sidebar">
<div id="_docs-search-box" class="search-box" hidden>
<label for="_docs-search">Search documentation</label>
<input id="_docs-search" type="search" placeholder="Find a page…" autocomplete="off" maxlength="128" aria-controls="_docs-results">
<p id="_docs-search-status" class="search-status" role="status"></p>
<ul id="_docs-results" class="search-results"></ul>
</div>
<details class="mobile-navigation" open><summary>Navigation</summary>
<nav aria-label="Documentation"><p class="nav-caption">CONTENTS</p><ul class="navigation">
{{if .GroupedNav}}{{template "navigation-items" .Navigation}}{{else}}{{range .Navigation}}<li><a href="{{.URL}}"{{if .Current}} aria-current="page"{{end}}>{{.Title}}</a></li>{{end}}{{end}}
</ul></nav></details>
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

const siteCSS = `:root{color-scheme:light dark;--bg:#fff;--panel:#f8fafc;--text:#172033;--muted:#5a687e;--line:#e3e8f0;--accent:#2563eb;--code:#f1f5f9;--space-1:4px;--space-2:8px;--space-3:12px;--space-4:16px;--space-6:24px;--space-8:32px;--space-12:48px;--reader-measure:42rem;--header-height:72px;--copy-target:40px}
@media(prefers-color-scheme:dark){:root{--bg:#0e1420;--panel:#131c2c;--text:#e4ebf5;--muted:#a4b1c8;--line:#27344b;--accent:#8eb7ff;--code:#1b273c}}
:root[data-theme=light]{color-scheme:light;--bg:#fff;--panel:#f8fafc;--text:#172033;--muted:#5a687e;--line:#e3e8f0;--accent:#2563eb;--code:#f1f5f9}
:root[data-theme=dark]{color-scheme:dark;--bg:#0e1420;--panel:#131c2c;--text:#e4ebf5;--muted:#a4b1c8;--line:#27344b;--accent:#8eb7ff;--code:#1b273c}
*{box-sizing:border-box}::selection{background:var(--accent);color:var(--bg)}input,textarea{caret-color:var(--accent)}input::placeholder,textarea::placeholder{color:var(--muted);opacity:1}body{margin:0;background:var(--bg);color:var(--text);font:16px/1.7 ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}a{color:var(--accent);text-underline-offset:.2em}button,input,textarea,select{font:inherit}button,summary{cursor:pointer}button{border:1px solid var(--line);border-radius:8px;background:var(--panel);color:var(--text);padding:8px 12px;min-height:40px;line-height:1.4}button:hover{border-color:var(--accent);background:var(--code)}button:active{background:var(--code)}button:disabled{cursor:default;opacity:.55}:focus-visible{outline:3px solid var(--accent);outline-offset:3px}[hidden]{display:none!important}.skip-link{position:fixed;top:-100px;left:20px;z-index:10;background:var(--bg);padding:10px}.skip-link:focus{top:8px}.topbar{height:var(--header-height);position:sticky;top:0;z-index:2;display:flex;align-items:center;justify-content:space-between;padding:0 var(--space-6);border-bottom:1px solid var(--line);background:var(--bg)}.brand{display:flex;align-items:center;gap:var(--space-3);min-width:0;overflow-wrap:anywhere;font-weight:700;font-size:18px;color:var(--text);text-decoration:none}.brand-mark{display:grid;place-items:center;width:32px;height:32px;flex:none;border-radius:8px;background:var(--accent);color:var(--bg);font-size:20px}.layout{max-width:1600px;margin:auto;display:grid;grid-template-columns:265px minmax(0,1fr) 220px;min-height:calc(100vh - var(--header-height))}.sidebar{padding:var(--space-8) var(--space-6);border-right:1px solid var(--line)}.sidebar,.toc{position:sticky;top:var(--header-height);align-self:start;height:calc(100vh - var(--header-height));overflow:auto}.nav-caption{font-size:12px;line-height:1.5;font-weight:650;letter-spacing:.04em;color:var(--muted);margin:var(--space-6) 0 var(--space-2)}.navigation,.toc ul,.search-results{list-style:none;padding:0;margin:0}.navigation a{display:block;padding:8px 12px;margin:4px 0;border-radius:8px;color:var(--muted);font-size:14px;line-height:1.5;text-decoration:none}.navigation a[aria-current=page]{color:var(--accent);background:var(--code);font-weight:650}.navigation a:hover{background:var(--code)}.mobile-navigation>summary{display:none}.mobile-navigation:not([open])>summary{display:block}.mobile-toc{display:none}.toolbar{flex:none;margin-inline-start:16px}.sidebar-foot{border-top:1px solid var(--line);margin-top:35px;padding-top:18px;color:var(--muted);font-size:12px}.search-box label{font-size:12px;color:var(--muted)}input[type=search]{display:block;margin:8px 0 0;width:100%;min-width:0;min-height:40px;padding:8px 12px;border:1px solid var(--line);border-radius:8px;background:var(--bg);color:var(--text);font-size:13px}.search-status{font-size:12px;line-height:1.5;color:var(--muted);margin:8px 0}.search-status:empty{display:none}.search-results a{display:block;padding:12px 0;font-size:13px;font-weight:600;text-decoration:none;overflow-wrap:anywhere}.search-results li+li{border-top:1px solid var(--line)}.search-results small{display:block;color:var(--muted);font-weight:400}.breadcrumb{font-size:12px;line-height:1.5;color:var(--muted);margin-bottom:32px}.breadcrumb span{padding:0 8px}main{padding:32px 48px;min-width:0}.layout main>article,.layout main>.breadcrumb,.layout main>.page-sequence,.layout main>.page-footer{width:100%;max-width:min(var(--docs-article-width,900px),var(--reader-measure));margin-inline:auto}.api-page main>article,.api-page main>.breadcrumb,.api-page main>.page-sequence,.api-page main>.page-footer{max-width:var(--docs-article-width,900px)}article{min-width:0;margin:auto}h1,h2,h3,h4,h5,h6{line-height:1.3;letter-spacing:-.02em;scroll-margin-top:96px;text-wrap:balance;overflow-wrap:anywhere}h1{font-size:36px;margin:0 0 24px}h2{font-size:26px;margin:48px 0 16px}h3{font-size:21px;margin:32px 0 12px}h4,h5,h6{margin:24px 0 8px}p,ul,ol{margin:16px 0}p{ text-wrap:pretty}li+li{margin-top:4px}.navigation li+li,.toc li+li,.mobile-toc li+li{margin-top:0}article> :first-child{margin-top:0}code,pre{font: .88em/1.65 ui-monospace,SFMono-Regular,Consolas,monospace}code{background:var(--code);padding:.16em .34em;border-radius:4px}pre{position:relative;padding:24px;border:1px solid var(--line);border-radius:8px;background:var(--panel);overflow:auto}pre code{padding:0;background:none;border-radius:0}pre:has(.copy-code){padding-top:calc(var(--copy-target) + 24px)}.copy-code{position:absolute;right:12px;top:12px;min-width:var(--copy-target);min-height:var(--copy-target);padding:4px 8px;font-size:12px}blockquote{margin:24px 0;padding:4px 24px;border-left:1px solid var(--accent);background:var(--panel);color:var(--muted)}table{display:block;overflow:auto;width:100%;border-collapse:collapse;font-size:14px;margin:24px 0}th,td{text-align:left;padding:12px 16px;border:1px solid var(--line);vertical-align:top}th{background:var(--panel);font-weight:600}img{max-width:100%;height:auto}hr{border:0;border-top:1px solid var(--line);margin:36px 0}.toc{padding:32px 24px 32px 0}.toc .nav-caption{margin-top:0}.toc a{display:block;padding:4px 0;font-size:13px;line-height:1.5;text-decoration:none;color:var(--muted);overflow-wrap:anywhere}.toc a:hover{color:var(--accent)}.toc-level-3,.toc-level-4,.toc-level-5,.toc-level-6{margin-left:12px}.page-footer{margin-top:48px;padding-top:24px;border-top:1px solid var(--line);color:var(--muted);font-size:12px}
.navigation ul{list-style:none;margin:0 0 0 12px;padding:0 0 0 8px;border-inline-start:1px solid var(--line)}.navigation .nav-group>summary{display:list-item;padding:8px 12px;color:var(--text);font-size:14px;font-weight:650;overflow-wrap:anywhere}.nav-group-description{margin:4px 12px 10px;font-size:12px;color:var(--muted);overflow-wrap:anywhere}.breadcrumb ol{display:flex;flex-wrap:wrap;gap:0;list-style:none;margin:0;padding:0}.breadcrumb li+li::before{content:"/";padding:0 10px}.navigation a{overflow-wrap:anywhere}
.docs-callout{--callout-accent:var(--accent);margin:24px 0;padding:16px 24px;border:1px solid var(--line);border-inline-start:1px solid var(--callout-accent);border-radius:8px;background:var(--panel);overflow-wrap:anywhere}.docs-callout-tip{--callout-accent:#16a34a}.docs-callout-info{--callout-accent:#0891b2}.docs-callout-warning{--callout-accent:#d97706}.docs-callout-danger{--callout-accent:#dc2626}.docs-callout-title{margin:0 0 8px;font-weight:700}.docs-callout>:last-child{margin-bottom:0}.docs-tabs{min-width:0;margin:24px 0}.docs-tab-links{display:flex;flex-wrap:wrap;gap:4px;margin:0;padding:8px;border:1px solid var(--line);border-radius:10px 10px 0 0;background:var(--panel)}.docs-tab-links>a{display:block;max-width:100%;padding:6px 12px;border:1px solid transparent;border-radius:7px;color:var(--muted);font-weight:600;font-size:14px;text-decoration:none;overflow-wrap:anywhere}.docs-tab-links>a:hover,.docs-tab-links>a[aria-selected=true]{color:var(--text);background:var(--bg);border-color:var(--line)}.docs-tab-panel{padding:24px;border:1px solid var(--line);border-top:0}.docs-tab-panel:last-child{border-radius:0 0 10px 10px}.docs-tab-panel>:first-child{margin-top:0}.docs-tab-panel>:last-child{margin-bottom:0}.docs-tab-title{font-weight:700}.docs-tabs-ready>.docs-tab-panel>.docs-tab-title{display:none}.docs-include{min-width:0}
@media print{.docs-tab-links{display:none}.docs-tabs-ready>.docs-tab-panel[hidden]{display:block!important}.docs-tabs-ready>.docs-tab-panel>.docs-tab-title{display:block}.docs-tab-panel{border:0;padding:8px 0}.docs-tab-title,.docs-callout-title{break-after:avoid}}
@media(max-width:1150px){.layout{grid-template-columns:240px minmax(0,1fr)}.toc{display:none}.mobile-toc{display:block;border-block:1px solid var(--line);margin:0 auto 32px;max-width:var(--reader-measure);font-size:14px}.mobile-toc>summary{padding:12px 0;font-weight:600}.mobile-toc ul{list-style:none;margin:0;padding:0 0 12px}.mobile-toc a{display:block;min-height:40px;padding:8px 0;color:var(--muted);text-decoration:none;overflow-wrap:anywhere}.api-page .mobile-toc{max-width:var(--docs-article-width,900px)}}@media(max-width:760px){:root{--header-height:64px}.topbar{padding:0 20px}.brand{font-size:15px}.brand-logo{max-width:120px}.layout{display:block}.sidebar{height:auto;position:static;border-right:0;border-bottom:1px solid var(--line);padding:16px 20px}.search-box{margin-bottom:12px}.mobile-navigation>summary,.mobile-navigation:not([open])>summary{display:list-item;padding:8px 0;font-size:14px;font-weight:600}.nav-caption{margin-top:16px}.sidebar-foot{display:none}main{padding:24px 20px}.breadcrumb{margin-bottom:24px}h1{font-size:30px}h2{font-size:24px;margin-top:40px}.mobile-toc{margin-bottom:24px}.navigation{max-height:50vh;overflow:auto}.navigation a{min-height:40px}.docs-tab-panel{padding:16px}pre{padding:16px}.page-sequence{flex-wrap:wrap}}
@media(max-width:760px),(pointer:coarse){:root{--copy-target:44px}}
@media(min-width:761px) and (max-width:1300px){main{padding-inline:32px}}@media(prefers-reduced-motion:no-preference){html{scroll-behavior:smooth}}@media print{.topbar,.sidebar,.toc,.mobile-toc,.copy-code,.skip-link,.breadcrumb{display:none}.layout{display:block}main{padding:0}pre,table{break-inside:avoid}}
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
