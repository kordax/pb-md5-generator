package site

// API styling is emitted only for pages with a normalized API reference.
const apiCSS = `
.api-reference{min-width:0;overflow-wrap:anywhere}
.api-reference :is(h2,h3,h4,h5){scroll-margin-top:5rem}
.api-reference h2{margin-top:2.5rem}
.api-group,.api-schema,.api-security-scheme{margin-block:1.5rem}
.api-operation{border:1px solid #8886;border-radius:.65rem;margin-block:1.5rem;padding:1.25rem;min-width:0}
.api-operation>h3{margin-top:0;font-size:1.2rem}
.api-operation-heading{display:flex;align-items:baseline;gap:.65rem;flex-wrap:wrap}
.api-method{display:inline-block;border:1px solid #527cb5;border-radius:.3rem;background:#527cb51a;padding:.15rem .5rem;font-family:ui-monospace,monospace;font-weight:700;font-size:.85rem;letter-spacing:.02em}
.api-path{font-weight:600;white-space:normal}
.api-summary{font-size:1.05rem}
.api-meta,.api-source{font-size:.85rem;opacity:.8}
.api-source code{white-space:normal}
.api-deprecated,.api-truncated{font-size:.85rem;border-inline-start:3px solid #b38231;padding-inline-start:.6rem}
.api-deprecated{color:inherit}
.api-model-link{font-size:.85rem}
.api-reference dl{display:grid;grid-template-columns:minmax(6rem,max-content) minmax(0,1fr);gap:.3rem 1rem}
.api-reference dt{font-weight:600}
.api-reference dd{margin:0;min-width:0}
.api-reference dd>p:first-child{margin-top:0}
.api-servers,.api-security,.api-media{margin-block:1rem}
.api-media{border-inline-start:2px solid #8885;padding-inline-start:1rem}
.api-media>p{margin-block:.4rem}
.api-table{max-width:100%;overflow-x:auto;margin-block:1rem}
.api-table table{width:100%;border-collapse:collapse;font-size:.9rem}
.api-table :is(th,td){border:1px solid #8885;padding:.65rem;vertical-align:top;text-align:start}
.api-table th{background:#8881}
.api-table td>:first-child{margin-top:0}
.api-table td>:last-child{margin-bottom:0}
.api-raw{margin-block:.75rem;min-width:0}
.api-raw>summary{cursor:pointer;font-size:.9rem;padding-block:.25rem}
.api-raw pre{max-height:24rem;overflow:auto;white-space:pre-wrap;overflow-wrap:anywhere;border:1px solid #8885;border-radius:.35rem;padding:.75rem;font-size:.8rem}
.api-reference button{font:inherit;cursor:pointer;border:1px solid #527cb5;border-radius:.35rem;padding:.45rem .8rem;background:#527cb51a;color:inherit}
.api-reference button:disabled{cursor:default;opacity:.55}
.api-reference :is(button,input,textarea,select):focus-visible{outline:2px solid #527cb5;outline-offset:3px}
.api-client{border-top:1px solid #8885;margin-top:1rem;padding-top:1rem;min-width:0}
.api-client[hidden]{display:none}
.api-client-form{display:grid;gap:.8rem;min-width:0}
.api-client-field{display:grid;gap:.3rem;min-width:0}
.api-client :is(input:not([type=checkbox]),textarea,select){box-sizing:border-box;display:block;width:100%;max-width:100%;border:1px solid #8888;border-radius:.3rem;background:transparent;color:inherit;font:inherit;padding:.45rem .6rem}
.api-client textarea{min-height:7rem;font-family:ui-monospace,monospace;font-size:.85rem;resize:vertical}
.api-client input[type=checkbox]{margin-inline-end:.45rem}
.api-client label{font-size:.9rem}
.api-client-credentials{border:1px solid #8885;border-radius:.35rem;padding:.75rem;display:grid;gap:.65rem}
.api-client-actions{display:flex;gap:.6rem;flex-wrap:wrap;margin-block:.8rem}
.api-client-preview,.api-client-response{white-space:pre-wrap;overflow-wrap:anywhere;overflow:auto;max-height:32rem;max-width:100%;border:1px solid #8885;border-radius:.35rem;padding:.75rem;font-size:.8rem}
.api-client-status{font-size:.9rem;min-height:1.4em;white-space:pre-wrap}
.api-client-status[role=alert]{border-inline-start:3px solid #b38231;padding-inline-start:.6rem}
@media(max-width:42rem){.api-operation{padding:.8rem}.api-table :is(th,td){padding:.45rem}.api-reference dl{grid-template-columns:1fr}.api-reference dd{margin-bottom:.5rem}.api-client-actions button{flex:1}}
@media print{.api-reference button,.api-client{display:none!important}.api-operation{break-inside:avoid}.api-raw pre{max-height:none}}
`
