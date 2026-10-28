package site

// API styling is emitted only for pages with a normalized API reference.
const apiCSS = `
.api-reference{min-width:0;overflow-wrap:anywhere}
.api-reference :is(h2,h3,h4,h5){scroll-margin-top:96px}
.api-reference h2{margin:48px 0 16px}
.api-group,.api-schema,.api-security-scheme{margin-block:32px}
.api-operation{border-top:1px solid var(--line);margin-block:32px;padding:32px 0 0;min-width:0}
.api-operation>h3{margin:0 0 12px;font-size:21px}
.api-operation-heading{display:flex;align-items:center;gap:12px;flex-wrap:wrap;margin:12px 0}
.api-method{display:inline-block;border:1px solid var(--line);border-radius:4px;background:var(--code);color:var(--text);padding:4px 8px;font-family:ui-monospace,monospace;font-weight:700;font-size:13px;line-height:1.5;letter-spacing:.02em}
.api-path{font-size:15px;font-weight:600;white-space:normal}
.api-meta,.api-source{font-size:13px;line-height:1.6;color:var(--muted);margin:8px 0}
.api-source code{white-space:normal}
.api-deprecated,.api-truncated{font-size:13px;border:1px solid var(--line);border-radius:4px;background:var(--panel);padding:8px 12px}
.api-deprecated{color:inherit}
.api-model-link{font-size:13px;margin:8px 0}
.api-reference dl{display:grid;grid-template-columns:minmax(6rem,max-content) minmax(0,1fr);gap:8px 24px}
.api-reference dt{font-weight:600}
.api-reference dd{margin:0;min-width:0}
.api-reference dd>p:first-child{margin-top:0}
.api-servers,.api-security,.api-media{margin-block:24px}
.api-servers p,.api-security p{margin:8px 0}
.api-media{min-width:0}
.api-media>p{margin:8px 0;font-size:14px}
.api-table{max-width:100%;overflow-x:auto;margin-block:24px}
.api-table table{display:table;width:100%;border-collapse:collapse;font-size:14px;line-height:1.6;margin:0}
.api-table :is(th,td){border:1px solid var(--line);padding:12px 16px;vertical-align:top;text-align:start}
.api-table th{background:var(--panel);font-size:13px;font-weight:600}
.api-table code{overflow-wrap:anywhere}
.api-response+.api-response{border-top:1px solid var(--line);margin-top:24px;padding-top:8px}
.api-schema,.api-security-scheme{border-top:1px solid var(--line);padding-top:24px}
.api-schema>h3,.api-security-scheme>h3{margin-top:0}
.api-table td>:first-child{margin-top:0}
.api-table td>:last-child{margin-bottom:0}
.api-raw{margin-block:12px;min-width:0}
.api-raw>summary{cursor:pointer;font-size:13px;line-height:1.6;padding-block:8px;color:var(--muted)}
.api-raw>summary:hover{color:var(--text)}
.api-raw pre{max-height:24rem;overflow:auto;white-space:pre-wrap;overflow-wrap:anywhere;border:1px solid var(--line);border-radius:var(--docs-radius,8px);padding:16px;font-size:13px;margin:8px 0 16px}
.api-raw pre:has(.copy-code){padding-top:calc(var(--copy-target) + 24px)}
.api-reference button{font:inherit;cursor:pointer;border:1px solid var(--line);border-radius:var(--docs-radius,8px);padding:8px 12px;background:var(--panel);color:var(--text);min-height:40px;font-size:14px}
.api-reference button:hover{border-color:var(--accent);background:var(--code)}
.api-reference button:disabled{cursor:default;opacity:.55}
.api-reference button.copy-code{min-width:var(--copy-target);min-height:var(--copy-target);padding:4px 8px;font-size:12px}
.api-reference :is(button,input,textarea,select):focus-visible{outline:3px solid var(--accent);outline-offset:3px}
.api-client{border-top:1px solid var(--line);margin-top:24px;padding-top:24px;min-width:0}
.api-client[hidden]{display:none}
.api-client-form{display:grid;gap:16px;min-width:0}
.api-client-field{display:grid;gap:8px;min-width:0}
.api-client :is(input:not([type=checkbox]),textarea,select){box-sizing:border-box;display:block;width:100%;max-width:100%;min-height:40px;border:1px solid var(--line);border-radius:var(--docs-radius,8px);background:var(--bg);color:var(--text);font:inherit;padding:8px 12px}
.api-client textarea{min-height:7rem;font-family:ui-monospace,monospace;font-size:14px;resize:vertical}
.api-client-field:has(>input[type=checkbox]){grid-template-columns:16px minmax(0,1fr);align-items:start;gap:8px}
.api-client-field>input[type=checkbox]{grid-area:1/1;width:16px;height:16px;margin:4px 0 0;accent-color:var(--accent)}
.api-client label{font-size:14px;font-weight:500}
.api-client-credentials{border:1px solid var(--line);border-radius:var(--docs-radius,8px);padding:16px;display:grid;gap:12px}
.api-client-actions{display:flex;gap:8px;flex-wrap:wrap;margin-block:16px}
.api-client-preview,.api-client-response{white-space:pre-wrap;overflow-wrap:anywhere;overflow:auto;max-height:32rem;max-width:100%;border:1px solid var(--line);border-radius:var(--docs-radius,8px);padding:16px;font-size:13px}
.api-client-status{font-size:14px;min-height:1.5em;white-space:pre-wrap}
.api-client-status[role=alert]{border:1px solid var(--line);background:var(--panel);padding:12px 16px}
@media(min-width:1280px){.api-media:has(>.api-example-preview){display:grid;grid-template-columns:minmax(180px,1fr) minmax(0,2fr);gap:0 24px;align-items:start}.api-media:has(>.api-example-preview)>p{grid-column:1}.api-media:has(>.api-example-preview)>.api-example-preview{grid-column:2;grid-row:1/span 3;margin:0;min-width:0}.api-media:has(>.api-example-preview)>.api-raw:not(.api-example-preview){grid-column:1/-1}}
@media(max-width:42rem){.api-operation{padding-top:24px}.api-table :is(th,td){padding:8px 12px}.api-reference dl{grid-template-columns:1fr;gap:4px}.api-reference dd{margin-bottom:12px}.api-client-actions button{flex:1}.api-table table{min-width:34rem}}
@media print{.api-reference button,.api-client{display:none!important}.api-operation{break-inside:avoid}.api-raw pre{max-height:none}}
`
