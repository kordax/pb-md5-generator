package site

import (
	"html/template"
)

type pageView struct {
	Project       Manifest
	Page          Page
	Navigation    []navItem
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
}

type navItem struct {
	Title   string
	URL     string
	Current bool
}

var pageStart = template.Must(template.New("start").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' https:; object-src 'none'; base-uri 'none'; form-action 'none';{{if .Interactive}} connect-src 'self' http: https:;{{end}}">
<meta name="description" content="{{.Project.Description}}">
<title>{{.Page.Title}} · {{.Project.Title}}</title>
<link rel="stylesheet" href="{{.Stylesheet}}">
<link rel="alternate" type="application/vnd.pb-docs.page+json" href="{{.ModelURL}}">
{{if .APIStylesheet}}<link rel="stylesheet" href="{{.APIStylesheet}}">{{end}}
<script src="{{.Script}}" data-search="{{.Search}}" defer></script>
{{if .APIScript}}<script src="{{.APIScript}}" defer></script>{{end}}
</head>
<body{{if .APIStylesheet}} class="api-page"{{end}}>
<a class="skip-link" href="#_docs-main">Skip to content</a>
<header class="topbar">
<a class="brand" href="{{.Root}}"><span class="brand-mark" aria-hidden="true">D</span>{{.Project.Title}}</a>
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
{{range .Navigation}}<li><a href="{{.URL}}"{{if .Current}} aria-current="page"{{end}}>{{.Title}}</a></li>{{end}}
</ul></nav></details>
</aside>
<main id="_docs-main" tabindex="-1"><div class="breadcrumb">{{.Project.Title}} <span aria-hidden="true">/</span> {{.Page.Title}}</div>
<article>{{if .NeedsTitle}}<h1>{{.Page.Title}}</h1>{{end}}
`))

var pageEnd = template.Must(template.New("end").Parse(`
</article><footer class="page-footer">{{.Project.Title}}</footer></main>
<aside class="toc"><nav aria-label="On this page"><p class="nav-caption">ON THIS PAGE</p><ul>
{{range .Headings}}<li class="toc-level-{{.Level}}"><a href="#{{.ID}}">{{.Title}}</a></li>{{end}}
</ul></nav></aside>
</div>
</body>
</html>
`))

func renderPage(manifest Manifest, current int, content pageContent) ([]byte, error) {
	page := manifest.Pages[current]
	view := pageView{
		Project:     manifest,
		Page:        page,
		Headings:    content.Headings,
		Root:        relativeURL(page.Path, "index.html"),
		Stylesheet:  relativeURL(page.Path, "_docs/site.css"),
		Script:      relativeURL(page.Path, "_docs/site.js"),
		Search:      relativeURL(page.Path, "_docs/search.json"),
		NeedsTitle:  true,
		ModelURL:    relativeURL(page.Path, pageDataPath(page)),
		Interactive: content.Interactive,
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
	for i, item := range manifest.Pages {
		view.Navigation = append(view.Navigation, navItem{Title: item.Title, URL: relativeURL(page.Path, item.Path), Current: i == current})
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

const siteCSS = `:root{color-scheme:light dark;--bg:#fff;--panel:#f8fafc;--text:#172033;--muted:#5a687e;--line:#e3e8f0;--accent:#2563eb;--code:#f1f5f9}
@media(prefers-color-scheme:dark){:root{--bg:#0e1420;--panel:#131c2c;--text:#e4ebf5;--muted:#a4b1c8;--line:#27344b;--accent:#8eb7ff;--code:#1b273c}}
:root[data-theme=light]{color-scheme:light;--bg:#fff;--panel:#f8fafc;--text:#172033;--muted:#5a687e;--line:#e3e8f0;--accent:#2563eb;--code:#f1f5f9}
:root[data-theme=dark]{color-scheme:dark;--bg:#0e1420;--panel:#131c2c;--text:#e4ebf5;--muted:#a4b1c8;--line:#27344b;--accent:#8eb7ff;--code:#1b273c}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:16px/1.7 ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}a{color:var(--accent);text-underline-offset:.2em}button,input{font:inherit}button,summary{cursor:pointer}button{border:1px solid var(--line);border-radius:7px;background:var(--panel);color:var(--text);padding:5px 12px}button:hover{border-color:var(--accent)}:focus-visible{outline:3px solid var(--accent);outline-offset:3px}[hidden]{display:none!important}.skip-link{position:fixed;top:-100px;left:20px;z-index:10;background:var(--bg);padding:10px}.skip-link:focus{top:8px}.topbar{height:74px;position:sticky;top:0;z-index:2;display:flex;align-items:center;justify-content:space-between;padding:0 32px;border-bottom:1px solid var(--line);background:var(--bg)}.brand{display:flex;align-items:center;gap:12px;font-weight:700;font-size:18px;color:var(--text);text-decoration:none}.brand-mark{display:grid;place-items:center;width:32px;height:32px;border-radius:9px;background:var(--accent);color:var(--bg);font-size:20px}.layout{max-width:1600px;margin:auto;display:grid;grid-template-columns:265px minmax(0,1fr) 220px;min-height:calc(100vh - 74px)}.sidebar{padding:30px 22px;border-right:1px solid var(--line)}.sidebar,.toc{position:sticky;top:74px;align-self:start;height:calc(100vh - 74px);overflow:auto}.nav-caption{font-size:11px;font-weight:750;letter-spacing:.12em;color:var(--muted);margin:24px 0 12px}.navigation,.toc ul,.search-results{list-style:none;padding:0;margin:0}.navigation a{display:block;padding:8px 12px;margin:3px 0;border-radius:7px;color:var(--muted);font-size:14px;text-decoration:none}.navigation a[aria-current=page]{color:var(--accent);background:var(--code);font-weight:650}.navigation a:hover{background:var(--code)}.mobile-navigation summary{display:none}.mobile-navigation:not([open]) summary{display:block}.sidebar-foot{border-top:1px solid var(--line);margin-top:35px;padding-top:18px;color:var(--muted);font-size:12px}.search-box label{font-size:12px;color:var(--muted)}input[type=search]{display:block;margin:6px 0;width:100%;min-width:0;padding:8px 10px;border:1px solid var(--line);border-radius:8px;background:var(--bg);color:var(--text);font-size:13px}.search-status{font-size:12px;color:var(--muted);margin:6px 0}.search-results a{display:block;padding:8px 0;font-size:13px;font-weight:600}.search-results small{display:block;color:var(--muted);font-weight:400}.breadcrumb{font-size:12px;color:var(--muted);margin-bottom:32px}.breadcrumb span{padding:0 10px}main{padding:38px clamp(22px,4vw,70px) 24px;min-width:0}article{max-width:900px;margin:auto}h1,h2,h3,h4,h5,h6{line-height:1.3;letter-spacing:-.02em;scroll-margin-top:100px}h1{font-size:clamp(30px,4vw,42px);margin:0 0 26px}h2{font-size:25px;margin-top:2em;padding-top:8px}h3{font-size:20px;margin-top:1.8em}p,ul,ol{margin:1em 0}code,pre{font: .88em/1.65 ui-monospace,SFMono-Regular,Consolas,monospace}code{background:var(--code);padding:.16em .34em;border-radius:4px}pre{position:relative;padding:22px;border:1px solid var(--line);border-radius:10px;background:var(--panel);overflow:auto}pre code{padding:0;background:none;border-radius:0}pre:has(.copy-code){padding-top:50px}.copy-code{position:absolute;right:10px;top:9px;font-size:11px}blockquote{margin:24px 0;padding:3px 20px;border-left:3px solid var(--accent);background:var(--panel);color:var(--muted)}table{display:block;overflow:auto;width:100%;border-collapse:collapse;font-size:14px;margin:24px 0}th,td{text-align:left;padding:12px 16px;border:1px solid var(--line);vertical-align:top}th{background:var(--panel);font-weight:600}img{max-width:100%;height:auto}hr{border:0;border-top:1px solid var(--line);margin:36px 0}.toc{padding:32px 18px}.toc a{display:block;padding:5px 0;font-size:12px;line-height:1.5;text-decoration:none;color:var(--muted)}.toc a:hover{color:var(--accent)}.toc-level-3,.toc-level-4,.toc-level-5,.toc-level-6{margin-left:12px}.page-footer{margin-top:70px;padding-top:20px;border-top:1px solid var(--line);color:var(--muted);font-size:12px}
.docs-callout{--callout-accent:var(--accent);margin:24px 0;padding:4px 20px 8px;border:1px solid var(--line);border-inline-start:4px solid var(--callout-accent);border-radius:10px;background:var(--panel);overflow-wrap:anywhere}.docs-callout-tip{--callout-accent:#16a34a}.docs-callout-info{--callout-accent:#0891b2}.docs-callout-warning{--callout-accent:#d97706}.docs-callout-danger{--callout-accent:#dc2626}.docs-callout-title{margin:.6em 0;font-weight:700}.docs-callout>:last-child{margin-bottom:.6em}.docs-tabs{min-width:0;margin:24px 0}.docs-tab-links{display:flex;flex-wrap:wrap;gap:6px;margin:0;padding:6px;border:1px solid var(--line);border-radius:10px 10px 0 0;background:var(--panel)}.docs-tab-links>a{display:block;max-width:100%;padding:6px 12px;border:1px solid transparent;border-radius:7px;color:var(--muted);font-weight:600;font-size:14px;text-decoration:none;overflow-wrap:anywhere}.docs-tab-links>a:hover,.docs-tab-links>a[aria-selected=true]{color:var(--text);background:var(--bg);border-color:var(--line)}.docs-tab-panel{padding:16px 20px;border:1px solid var(--line);border-top:0}.docs-tab-panel:last-child{border-radius:0 0 10px 10px}.docs-tab-panel>:first-child{margin-top:0}.docs-tab-panel>:last-child{margin-bottom:0}.docs-tab-title{font-weight:700}.docs-tabs-ready>.docs-tab-panel>.docs-tab-title{display:none}.docs-include{min-width:0}
@media print{.docs-tab-links{display:none}.docs-tabs-ready>.docs-tab-panel[hidden]{display:block!important}.docs-tabs-ready>.docs-tab-panel>.docs-tab-title{display:block}.docs-tab-panel{border:0;padding:8px 0}.docs-tab-title,.docs-callout-title{break-after:avoid}}
@media(max-width:1150px){.layout{grid-template-columns:240px minmax(0,1fr)}.toc{display:none}}@media(max-width:760px){.topbar{height:64px;padding:0 18px}.brand{font-size:15px}.layout{display:block}.sidebar{height:auto;position:static;border-right:0;border-bottom:1px solid var(--line);padding:15px 20px}.mobile-navigation summary{display:block;font-size:14px}.nav-caption{margin-top:12px}.sidebar-foot{display:none}main{padding:26px 20px}.breadcrumb{margin-bottom:24px}.navigation{max-height:210px;overflow:auto}}@media(prefers-reduced-motion:no-preference){html{scroll-behavior:smooth}}@media print{.topbar,.sidebar,.toc,.copy-code,.skip-link,.breadcrumb{display:none}.layout{display:block}main{padding:0}pre,table{break-inside:avoid}}
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
    let mobileOpen = navigation.open;
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
