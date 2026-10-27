package site

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/document"
)

const (
	maxAPIRawPreview    = 64 << 10
	maxAPISchemaSummary = 1 << 20
	maxAPIProperties    = 32
	maxAPIText          = 32 << 10
)

type apiRenderer struct {
	ctx              context.Context
	api              *document.API
	current          int
	manifest         Manifest
	modelURL         string
	content          pageContent
	buffer           boundedBuffer
	targets          map[string]string
	schemas          map[string]document.Schema
	security         map[string]string
	emitted          map[string]bool
	nodes, fragments int
	err              error
}

// renderAPI keeps referenced schemas as stable nodes instead of expanding trees.
func renderAPI(ctx context.Context, api *document.API, current int, manifest Manifest, dataPath string) (pageContent, error) {
	if err := ctx.Err(); err != nil {
		return pageContent{}, err
	}
	if api == nil {
		return pageContent{}, fmt.Errorf("API reference is missing its contract")
	}
	if current < 0 || current >= len(manifest.Pages) {
		return pageContent{}, fmt.Errorf("API reference has an invalid current page")
	}
	if dataPath == "" {
		return pageContent{}, fmt.Errorf("API reference is missing its page model path")
	}
	page := manifest.Pages[current]
	r := &apiRenderer{ctx: ctx, api: api, current: current, manifest: manifest, modelURL: relativeURL(page.Path, dataPath), buffer: boundedBuffer{limit: maxRenderedBytes}, targets: make(map[string]string), schemas: make(map[string]document.Schema), security: make(map[string]string), emitted: make(map[string]bool)}
	r.content = pageContent{Anchors: make(map[string]bool), APIData: r.modelURL}
	for _, operation := range api.Operations {
		if !r.step() {
			break
		}
		r.target(operation.ID, operation.Anchor)
	}
	for _, schema := range api.Schemas {
		if !r.step() {
			break
		}
		r.target(schema.ID, schema.Anchor)
		r.schemas[schema.ID] = schema
	}
	for _, scheme := range api.SecuritySchemes {
		if !r.step() {
			break
		}
		anchor := document.APIAnchor("api-security", scheme.ID)
		if scheme.ID == "" || r.security[scheme.ID] != "" {
			r.err = fmt.Errorf("API reference has an empty or duplicate security scheme ID")
			break
		}
		r.security[scheme.ID] = anchor
		if err := addAnchor(r.content.Anchors, anchor); err != nil {
			r.err = err
		}
	}
	if r.err != nil {
		return pageContent{}, r.err
	}
	r.raw(`<article class="api-reference">`)
	title := api.Title
	if title == "" {
		title = page.Title
	}
	r.heading(1, document.APIAnchor("api", pageIdentity(page)), title)
	r.raw(`<p class="api-meta">`)
	r.text(strings.ToUpper(api.Protocol))
	for _, value := range []string{api.Format, api.Version, api.APIVersion} {
		if value != "" {
			r.raw(` <span aria-hidden="true">·</span> `)
			r.text(value)
		}
	}
	r.raw(`</p>`)
	r.source(api.Source)
	r.modelLink("Complete page model (JSON)")
	r.markdown(api.Description, api.Source.File, "root")
	if api.Protocol == "http" {
		r.servers(api.Servers, api.Source.File, "root-servers")
	}
	r.securityRequirements(api.Security, "Declared authentication")
	r.operations()
	r.securitySchemes()
	if len(api.Schemas) > 0 {
		r.heading(2, document.APIAnchor("api-schemas", pageIdentity(page)), "Schemas")
		for _, schema := range api.Schemas {
			if !r.step() {
				break
			}
			r.schema(schema)
		}
	}
	r.raw(`</article>`)
	if r.err != nil {
		return pageContent{}, r.err
	}
	r.content.HTML = r.buffer.String()
	r.content.Text = strings.Clone(strings.TrimSpace(r.content.Text))
	return r.content, nil
}

func (r *apiRenderer) step() bool {
	if r.err != nil {
		return false
	}
	if err := r.ctx.Err(); err != nil {
		r.err = err
		return false
	}
	r.nodes++
	if r.nodes > maxPageNodes {
		r.err = fmt.Errorf("API reference exceeds %d content nodes", maxPageNodes)
		return false
	}
	return true
}

func (r *apiRenderer) raw(value string) {
	if r.err != nil {
		return
	}
	if err := r.ctx.Err(); err != nil {
		r.err = err
		return
	}
	_, r.err = r.buffer.WriteString(value)
}

func (r *apiRenderer) text(value string) {
	if r.err != nil {
		return
	}
	if !utf8.ValidString(value) {
		r.err = fmt.Errorf("API reference contains invalid UTF-8")
		return
	}
	if remaining := maxAPIText - len(r.content.Text); remaining > 0 {
		r.content.Text += " "
		r.content.Text += truncateAPIText(value, remaining-1)
	}
	for len(value) > 0 && r.err == nil {
		part := truncateAPIText(value, 4096)
		r.raw(html.EscapeString(part))
		value = value[len(part):]
	}
}

func truncateAPIText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	if limit < 0 {
		limit = 0
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func (r *apiRenderer) target(id, anchor string) {
	if id == "" || anchor == "" || r.targets[id] != "" {
		r.err = fmt.Errorf("API reference has an empty or duplicate operation/schema identity")
		return
	}
	if err := addAnchor(r.content.Anchors, anchor); err != nil {
		r.err = err
		return
	}
	r.targets[id] = anchor
}

func (r *apiRenderer) anchor(id string) {
	if r.err != nil {
		return
	}
	if r.emitted[id] {
		r.err = fmt.Errorf("duplicate API reference anchor %q", id)
		return
	}
	if !r.content.Anchors[id] {
		if err := addAnchor(r.content.Anchors, id); err != nil {
			r.err = err
			return
		}
	}
	r.emitted[id] = true
}

func (r *apiRenderer) heading(level int, id, title string) {
	if !r.step() {
		return
	}
	if len(r.content.Headings) >= maxPageHeadings {
		r.err = fmt.Errorf("API reference exceeds %d headings", maxPageHeadings)
		return
	}
	r.anchor(id)
	r.raw(`<h` + strconv.Itoa(level) + ` id="`)
	r.text(id)
	r.raw(`">`)
	r.text(title)
	r.raw(`</h` + strconv.Itoa(level) + `>`)
	r.content.Headings = append(r.content.Headings, heading{Title: title, ID: id, Level: level})
}

func (r *apiRenderer) modelLink(label string) {
	r.raw(`<p class="api-model-link"><a href="`)
	r.text(r.modelURL)
	r.raw(`">`)
	r.text(label)
	r.raw(`</a></p>`)
}

func (r *apiRenderer) source(location document.Location) {
	encoded, err := json.Marshal(location)
	if err != nil {
		r.err = fmt.Errorf("encode API source location: %w", err)
		return
	}
	r.raw(`<p class="api-source">Source: <code>`)
	r.text(string(encoded))
	r.raw(`</code></p>`)
}

func (r *apiRenderer) markdown(source, file, seed string) {
	if source == "" || !r.step() {
		return
	}
	r.fragments++
	scope := document.APIAnchor("api-description", seed+":"+strconv.Itoa(r.fragments))
	extra, err := compileMarkdownScoped([]byte(source), r.current, r.manifest, scope, func(destination string) (string, error) {
		if anchor := r.knownTarget(destination, file); anchor != "" {
			return "#" + anchor, nil
		}
		return destination, nil
	})
	if err != nil {
		r.err = fmt.Errorf("API description: %w", err)
		return
	}
	if err := r.ctx.Err(); err != nil {
		r.err = err
		return
	}
	if len(extra.Headings) > maxPageHeadings-len(r.content.Headings) {
		r.err = fmt.Errorf("API reference exceeds %d headings", maxPageHeadings)
		return
	}
	if err := mergePageContent(&r.content, extra); err != nil {
		r.err = err
		return
	}
	r.raw(extra.HTML)
}

func (r *apiRenderer) knownTarget(value, source string) string {
	if target := r.targets[value]; target != "" {
		return target
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment == "" {
		return ""
	}
	file := source
	if parsed.Path != "" {
		file = path.Clean(path.Join(path.Dir(source), parsed.Path))
	}
	return r.targets[file+"#"+parsed.Fragment]
}

func (r *apiRenderer) schemaLink(id string) {
	if id == "" {
		r.text("Not declared")
		return
	}
	schema, known := r.schemas[id]
	label := id
	if known && schema.Name != "" {
		label = schema.Name
	}
	if anchor := r.targets[id]; known && anchor != "" {
		r.raw(`<a href="#`)
		r.text(anchor)
		r.raw(`"><code>`)
		r.text(label)
		r.raw(`</code></a>`)
		r.content.References = append(r.content.References, reference{page: r.current, fragment: anchor})
		return
	}
	r.raw(`<code>`)
	r.text(id)
	r.raw(`</code> <span class="api-meta">(unresolved schema)</span>`)
}

type apiOperationGroup struct {
	name       string
	operations []document.Operation
}

func (r *apiRenderer) operations() {
	groups := make([]apiOperationGroup, 0)
	indices := make(map[string]int)
	for _, operation := range r.api.Operations {
		if !r.step() {
			return
		}
		name := "Operations"
		if r.api.Protocol == "grpc" && operation.Service != "" {
			name = operation.Service
		} else if len(operation.Tags) > 0 {
			name = operation.Tags[0]
		}
		index, ok := indices[name]
		if !ok {
			index = len(groups)
			indices[name] = index
			groups = append(groups, apiOperationGroup{name: name})
		}
		groups[index].operations = append(groups[index].operations, operation)
	}
	for _, group := range groups {
		if !r.step() {
			return
		}
		r.raw(`<section class="api-group">`)
		r.heading(2, document.APIAnchor("api-group", group.name), group.name)
		for _, tag := range r.api.Tags {
			if !r.step() {
				return
			}
			if tag.Name != group.name {
				continue
			}
			if tag.Summary != "" {
				r.raw(`<p>`)
				r.text(tag.Summary)
				r.raw(`</p>`)
			}
			if tag.Parent != "" || tag.Kind != "" {
				r.raw(`<p class="api-meta">`)
				r.text("Parent: " + tag.Parent + "; kind: " + tag.Kind)
				r.raw(`</p>`)
			}
			r.markdown(tag.Description, r.api.Source.File, "tag:"+tag.Name)
			break
		}
		for _, operation := range group.operations {
			if !r.step() {
				return
			}
			r.operation(operation)
		}
		r.raw(`</section>`)
	}
}

func (r *apiRenderer) operation(operation document.Operation) {
	r.raw(`<section class="api-operation">`)
	label := operation.Method + " " + operation.Path
	if r.api.Protocol == "grpc" {
		label = operation.Service + "." + operation.RPCMethod
	}
	r.heading(3, operation.Anchor, strings.TrimSpace(label))
	r.raw(`<p class="api-operation-heading"><span class="api-method">`)
	if r.api.Protocol == "grpc" {
		r.text("RPC")
	} else {
		r.text(operation.Method)
	}
	r.raw(`</span> <code class="api-path">`)
	r.text(operation.Path)
	r.raw(`</code>`)
	if operation.Deprecated {
		r.raw(` <strong class="api-deprecated">Deprecated</strong>`)
	}
	r.raw(`</p>`)
	if operation.Summary != "" {
		r.raw(`<p class="api-summary">`)
		r.text(operation.Summary)
		r.raw(`</p>`)
	}
	r.source(operation.Source)
	r.raw(`<p class="api-meta">Operation: <code>`)
	r.text(operation.ID)
	r.raw(`</code>`)
	if operation.Interaction != "" {
		r.raw(` · Interaction: `)
		r.text(operation.Interaction)
	}
	if len(operation.Tags) > 0 {
		r.raw(` · Tags: `)
		r.text(strings.Join(operation.Tags, ", "))
	}
	r.raw(`</p>`)
	r.markdown(operation.Description, operation.Source.File, "operation:"+operation.ID)
	if r.api.Protocol == "grpc" {
		r.raw(`<dl class="api-rpc"><dt>Input schema</dt><dd>`)
		r.schemaLink(operation.InputSchema)
		r.raw(`</dd><dt>Output schema</dt><dd>`)
		r.schemaLink(operation.OutputSchema)
		r.raw(`</dd><dt>Client streaming</dt><dd>`)
		r.text(strconv.FormatBool(operation.ClientStreaming))
		r.raw(`</dd><dt>Server streaming</dt><dd>`)
		r.text(strconv.FormatBool(operation.ServerStreaming))
		r.raw(`</dd></dl>`)
	} else {
		if operation.Interaction == "webhook" || operation.Interaction == "callback" {
			r.raw(`<p class="api-meta">Incoming interaction; this reference is not an API server endpoint.</p>`)
		}
		r.servers(operation.Servers, operation.Source.File, operation.ID+":servers")
		r.parameters(operation.Parameters, operation.Source.File, operation.ID+":parameters")
		if operation.RequestBody != nil {
			r.heading(4, document.APIAnchor("api-body", operation.ID), "Request body")
			r.raw(`<p>`)
			if operation.RequestBody.Required {
				r.text("Required")
			} else {
				r.text("Optional")
			}
			r.raw(`</p>`)
			r.markdown(operation.RequestBody.Description, operation.Source.File, operation.ID+":body")
			r.media(operation.RequestBody.Content, operation.Source.File, operation.ID+":body")
		}
		if len(operation.Responses) > 0 {
			r.heading(4, document.APIAnchor("api-responses", operation.ID), "Responses")
			for index, response := range operation.Responses {
				if !r.step() {
					return
				}
				seed := operation.ID + ":response:" + strconv.Itoa(index)
				r.raw(`<section class="api-response">`)
				r.heading(5, document.APIAnchor("api-response", seed), response.Status)
				r.markdown(response.Description, operation.Source.File, seed)
				r.parameters(response.Headers, operation.Source.File, seed+":headers")
				r.media(response.Content, operation.Source.File, seed)
				r.rawPreview("Original response metadata", string(response.Raw))
				r.raw(`</section>`)
			}
		}
	}
	r.securityRequirements(operation.Security, "Effective authentication")
	r.rawPreview("Original operation metadata", string(operation.Raw))
	if (r.manifest.API == nil || r.manifest.API.Enabled) && r.api.Protocol == "http" && (operation.Interaction == "" || operation.Interaction == "request") {
		r.raw(`<button type="button" data-api-operation="`)
		r.text(operation.ID)
		r.raw(`" data-api-url="`)
		r.text(r.modelURL)
		r.raw(`">Try request</button><div class="api-client" data-api-client hidden></div>`)
		r.content.Interactive = true
	}
	r.raw(`</section>`)
}

func (r *apiRenderer) servers(servers []document.Server, file, seed string) {
	r.raw(`<div class="api-servers"><p><strong>Servers</strong></p>`)
	if len(servers) == 0 {
		r.raw(`<p class="api-meta">No effective server is declared.</p>`)
	}
	for index, server := range servers {
		if !r.step() {
			return
		}
		r.raw(`<div><code>`)
		r.text(server.URL)
		r.raw(`</code>`)
		if server.Name != "" {
			r.raw(` <span>`)
			r.text(server.Name)
			r.raw(`</span>`)
		}
		r.markdown(server.Description, file, seed+":"+strconv.Itoa(index))
		if len(server.Variables) > 0 {
			r.raw(`<dl>`)
			for _, variable := range server.Variables {
				if !r.step() {
					return
				}
				r.raw(`<dt><code>`)
				r.text(variable.Name)
				r.raw(`</code></dt><dd>Default: <code>`)
				r.text(variable.Default)
				r.raw(`</code>`)
				if len(variable.Enum) > 0 {
					r.raw(`; choices: `)
					r.text(strings.Join(variable.Enum, ", "))
				}
				r.markdown(variable.Description, file, seed+":variable:"+variable.Name)
				r.raw(`</dd>`)
			}
			r.raw(`</dl>`)
		}
		r.raw(`</div>`)
	}
	r.raw(`</div>`)
}

func (r *apiRenderer) parameters(parameters []document.Parameter, file, seed string) {
	if len(parameters) == 0 {
		return
	}
	r.raw(`<div class="api-table"><table><thead><tr><th>Name / location</th><th>Requirement</th><th>Schema</th><th>Serialization / description</th></tr></thead><tbody>`)
	for index, parameter := range parameters {
		if !r.step() {
			return
		}
		r.raw(`<tr><td><code>`)
		r.text(parameter.Name)
		r.raw(`</code><br>`)
		r.text(parameter.In)
		r.raw(`</td><td>`)
		if parameter.Required {
			r.text("Required")
		} else {
			r.text("Optional")
		}
		if parameter.Deprecated {
			r.raw(`<br><strong class="api-deprecated">Deprecated</strong>`)
		}
		r.raw(`</td><td>`)
		r.schemaLink(parameter.Schema)
		r.raw(`</td><td>`)
		style := parameter.Style
		if style == "" {
			style = "default"
		}
		r.raw(`<p class="api-meta">Style: `)
		r.text(style)
		if parameter.Explode != nil {
			r.text("; explode: " + strconv.FormatBool(*parameter.Explode))
		} else {
			r.text("; explode: default")
		}
		r.text("; allowReserved: " + strconv.FormatBool(parameter.AllowReserved) + "; allowEmptyValue: " + strconv.FormatBool(parameter.AllowEmptyValue))
		r.raw(`</p>`)
		key := seed + ":" + strconv.Itoa(index)
		r.markdown(parameter.Description, file, key)
		r.media(parameter.Content, file, key+":content")
		r.examples(parameter.ExampleJSON, parameter.Example, parameter.Examples, file, key+":example")
		r.rawPreview("Original parameter metadata", string(parameter.Raw))
		r.raw(`</td></tr>`)
	}
	r.raw(`</tbody></table></div>`)
}

func (r *apiRenderer) media(media []document.MediaType, file, seed string) {
	for index, item := range media {
		if !r.step() {
			return
		}
		r.raw(`<div class="api-media"><p>Media type: <code>`)
		r.text(item.Name)
		r.raw(`</code></p><p>Schema: `)
		r.schemaLink(item.Schema)
		r.raw(`</p>`)
		if item.ItemSchema != "" {
			r.raw(`<p>Stream item schema: `)
			r.schemaLink(item.ItemSchema)
			r.raw(`</p>`)
		}
		r.examples(item.ExampleJSON, item.Example, item.Examples, file, seed+":"+strconv.Itoa(index))
		r.rawPreview("Media encoding", string(item.Encoding))
		r.raw(`</div>`)
	}
}

func (r *apiRenderer) examples(companion string, raw json.RawMessage, examples []document.Example, file, seed string) {
	if companion != "" {
		r.rawPreview("Example", companion)
	} else if len(raw) > 0 {
		r.rawPreview("Example", string(raw))
	}
	for index, example := range examples {
		if !r.step() {
			return
		}
		r.raw(`<div class="api-example"><p><strong>`)
		r.text(example.Name)
		if example.Summary != "" {
			r.text(": " + example.Summary)
		}
		r.raw(`</strong></p>`)
		r.markdown(example.Description, file, seed+":"+strconv.Itoa(index))
		switch {
		case example.SerializedValue != nil:
			if *example.SerializedValue == "" {
				r.raw(`<p>Serialized example: empty wire text.</p>`)
			} else {
				r.rawPreview("Serialized example (exact wire text)", *example.SerializedValue)
			}
		case example.ValueJSON != "":
			r.rawPreview("Example value", example.ValueJSON)
		case len(example.DataValue) > 0:
			r.rawPreview("Example value", string(example.DataValue))
		case len(example.Value) > 0:
			r.rawPreview("Example value", string(example.Value))
		}
		if example.ExternalValue != "" {
			r.raw(`<p>External example (not fetched): `)
			r.externalLink(example.ExternalValue)
			r.raw(`</p>`)
		}
		r.rawPreview("Original example metadata", string(example.Raw))
		r.raw(`</div>`)
	}
}

func (r *apiRenderer) externalLink(value string) {
	parsed, err := url.Parse(value)
	if err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" && parsed.User == nil {
		r.raw(`<a href="`)
		r.text(parsed.String())
		r.raw(`" rel="noreferrer noopener">`)
		r.text(value)
		r.raw(`</a>`)
		return
	}
	r.raw(`<code>`)
	r.text(value)
	r.raw(`</code>`)
}

func (r *apiRenderer) securityRequirements(requirements []document.SecurityRequirement, label string) {
	r.raw(`<div class="api-security"><p><strong>`)
	r.text(label)
	r.raw(`</strong></p>`)
	if len(requirements) == 0 {
		r.raw(`<p>Authentication not required.</p></div>`)
		return
	}
	r.raw(`<p>Choose one alternative (OR). Every scheme within an alternative is required (AND).</p><ol>`)
	for _, requirement := range requirements {
		if !r.step() {
			return
		}
		r.raw(`<li>`)
		if len(requirement) == 0 {
			r.text("Anonymous alternative")
		}
		names := make([]string, 0, len(requirement))
		for name := range requirement {
			if !r.step() {
				return
			}
			names = append(names, name)
		}
		sort.Strings(names)
		for index, name := range names {
			if !r.step() {
				return
			}
			if index > 0 {
				r.raw(` <strong>AND</strong> `)
			}
			if anchor := r.security[name]; anchor != "" {
				r.raw(`<a href="#`)
				r.text(anchor)
				r.raw(`"><code>`)
				r.text(name)
				r.raw(`</code></a>`)
				r.content.References = append(r.content.References, reference{page: r.current, fragment: anchor})
			} else {
				r.raw(`<code>`)
				r.text(name)
				r.raw(`</code> (unresolved scheme)`)
			}
			if len(requirement[name]) > 0 {
				r.text("; scopes: " + strings.Join(requirement[name], ", "))
			}
		}
		r.raw(`</li>`)
	}
	r.raw(`</ol></div>`)
}

func (r *apiRenderer) securitySchemes() {
	if len(r.api.SecuritySchemes) == 0 {
		return
	}
	r.heading(2, document.APIAnchor("api-security-schemes", pageIdentity(r.manifest.Pages[r.current])), "Security schemes")
	for _, scheme := range r.api.SecuritySchemes {
		if !r.step() {
			return
		}
		r.raw(`<section class="api-security-scheme">`)
		r.heading(3, r.security[scheme.ID], scheme.ID)
		r.raw(`<dl>`)
		for _, field := range [][2]string{{"Type", scheme.Type}, {"HTTP scheme", scheme.Scheme}, {"Name", scheme.Name}, {"Location", scheme.In}, {"Bearer format", scheme.BearerFormat}} {
			if field[1] == "" {
				continue
			}
			r.raw(`<dt>`)
			r.text(field[0])
			r.raw(`</dt><dd><code>`)
			r.text(field[1])
			r.raw(`</code></dd>`)
		}
		r.raw(`</dl>`)
		r.markdown(scheme.Description, r.api.Source.File, "security:"+scheme.ID)
		if scheme.OpenIDConnectURL != "" {
			r.raw(`<p>OpenID Connect discovery: `)
			r.externalLink(scheme.OpenIDConnectURL)
			r.raw(`</p>`)
		}
		r.rawPreview("OAuth flows", string(scheme.Flows))
		r.rawPreview("Original security metadata", string(scheme.Raw))
		r.raw(`</section>`)
	}
}

func (r *apiRenderer) rawPreview(label, value string) {
	if value == "" || !r.step() {
		return
	}
	truncated := len(value) > maxAPIRawPreview
	r.raw(`<details class="api-raw"><summary>`)
	r.text(label)
	if truncated {
		r.text(" (preview truncated at 64 KiB)")
	}
	r.raw(`</summary><pre><code>`)
	r.text(truncateAPIText(value, maxAPIRawPreview))
	r.raw(`</code></pre>`)
	if truncated {
		r.raw(`<p class="api-truncated">This is a partial preview. The complete value is retained in the page model.</p>`)
		r.modelLink("Read the complete page model (JSON)")
	}
	r.raw(`</details>`)
}

func (r *apiRenderer) schema(schema document.Schema) {
	r.raw(`<section class="api-schema">`)
	title := schema.Name
	if title == "" {
		title = schema.ID
	}
	r.heading(3, schema.Anchor, title)
	r.raw(`<p class="api-meta">Schema ID: <code>`)
	r.text(schema.ID)
	r.raw(`</code></p>`)
	r.source(schema.Source)
	var definition map[string]json.RawMessage
	if len(schema.Definition) > maxAPISchemaSummary {
		r.raw(`<p class="api-truncated">Structured summary omitted because this definition exceeds 1 MiB. Use the raw preview or complete page model.</p>`)
	} else if err := json.Unmarshal(schema.Definition, &definition); err != nil {
		r.raw(`<p class="api-meta">This definition has no object summary; its exact JSON is shown below.</p>`)
	} else {
		for _, key := range []string{"$schema", "$id", "$anchor", "$dynamicAnchor", "type", "format", "nullable", "const", "enum", "readOnly", "writeOnly", "additionalProperties", "unevaluatedProperties", "required"} {
			if value := definition[key]; len(value) > 0 {
				r.rawPreview(key, string(value))
			}
		}
		var description string
		if json.Unmarshal(definition["description"], &description) == nil {
			r.markdown(description, schema.Source.File, "schema:"+schema.ID)
		}
		var compositions []string
		for _, key := range []string{"allOf", "anyOf", "oneOf", "not", "if", "then", "else", "dependentSchemas", "prefixItems", "items", "contains"} {
			if len(definition[key]) > 0 {
				compositions = append(compositions, key)
			}
		}
		if len(compositions) > 0 {
			r.raw(`<p class="api-meta">Composition / applicators: `)
			r.text(strings.Join(compositions, ", "))
			r.raw(`. Preserved in the raw definition without expanding referenced trees.</p>`)
		}
		r.properties(schema, definition)
		if schema.ExampleJSON == "" {
			r.rawPreview("Schema example", string(definition["example"]))
		}
		if schema.DefaultJSON == "" {
			r.rawPreview("Schema default", string(definition["default"]))
		}
		r.rawPreview("Schema examples", string(definition["examples"]))
	}
	r.rawPreview("Schema example", schema.ExampleJSON)
	r.rawPreview("Schema default", schema.DefaultJSON)
	if len(schema.References) > 0 {
		r.raw(`<div class="api-schema-references"><p><strong>References</strong></p><ul>`)
		for _, ref := range schema.References {
			if !r.step() {
				return
			}
			r.raw(`<li><code>`)
			r.text(ref.Pointer)
			r.raw(`</code>: <code>`)
			r.text(ref.Reference)
			r.raw(`</code> → `)
			r.schemaLink(ref.Target)
			if ref.Dynamic {
				r.raw(` <span class="api-meta">Dynamic reference; this link is its static fallback, not an evaluation of dynamic scope.</span>`)
			}
			r.raw(`</li>`)
		}
		r.raw(`</ul></div>`)
	}
	r.rawPreview("Raw schema definition", string(schema.Definition))
	r.raw(`</section>`)
}

func (r *apiRenderer) properties(schema document.Schema, definition map[string]json.RawMessage) {
	var properties map[string]json.RawMessage
	if json.Unmarshal(definition["properties"], &properties) != nil || len(properties) == 0 {
		return
	}
	var requiredNames []string
	_ = json.Unmarshal(definition["required"], &requiredNames)
	required := make(map[string]bool, len(requiredNames))
	for _, name := range requiredNames {
		if !r.step() {
			return
		}
		required[name] = true
	}
	names := make([]string, 0, len(properties))
	for name := range properties {
		if !r.step() {
			return
		}
		names = append(names, name)
	}
	sort.Strings(names)
	r.raw(`<div class="api-table api-schema-properties"><table><thead><tr><th>Property</th><th>Type / reference</th><th>Requirement</th><th>Description</th></tr></thead><tbody>`)
	for index, name := range names {
		if index >= maxAPIProperties || !r.step() {
			break
		}
		var property map[string]json.RawMessage
		_ = json.Unmarshal(properties[name], &property)
		r.raw(`<tr><td><code>`)
		r.text(name)
		r.raw(`</code></td><td>`)
		if value := property["type"]; len(value) > 0 {
			r.text(string(value))
		}
		var ref string
		if json.Unmarshal(property["$ref"], &ref) == nil {
			if anchor := r.knownTarget(ref, schema.Source.File); anchor != "" {
				r.raw(`<a href="#`)
				r.text(anchor)
				r.raw(`"><code>`)
				r.text(ref)
				r.raw(`</code></a>`)
				r.content.References = append(r.content.References, reference{page: r.current, fragment: anchor})
			} else {
				r.raw(`<code>`)
				r.text(ref)
				r.raw(`</code>`)
			}
		}
		r.raw(`</td><td>`)
		if required[name] {
			r.text("Required")
		} else {
			r.text("Optional")
		}
		r.raw(`</td><td>`)
		var description string
		if json.Unmarshal(property["description"], &description) == nil {
			r.markdown(description, schema.Source.File, schema.ID+":property:"+name)
		}
		r.raw(`</td></tr>`)
	}
	r.raw(`</tbody></table></div>`)
	if len(names) > maxAPIProperties {
		r.raw(`<p class="api-truncated">Showing the first 32 properties in name order out of `)
		r.text(strconv.Itoa(len(names)))
		r.raw(`. Nested and remaining properties are retained in the raw definition and complete page model.</p>`)
	}
}
