package document

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// API retains protocol semantics separately from the rendered reference page.
// Documents keeps the bounded source contracts, including unnormalized fields.
type API struct {
	ID              string                `json:"id"`
	Protocol        string                `json:"protocol"`
	Format          string                `json:"format"`
	Version         string                `json:"version"`
	APIVersion      string                `json:"apiVersion,omitempty"`
	Title           string                `json:"title"`
	Description     string                `json:"description,omitempty"`
	Source          Location              `json:"source"`
	Servers         []Server              `json:"servers"`
	Tags            []Tag                 `json:"tags,omitempty"`
	Operations      []Operation           `json:"operations"`
	Schemas         []Schema              `json:"schemas"`
	SecuritySchemes []SecurityScheme      `json:"securitySchemes,omitempty"`
	Security        []SecurityRequirement `json:"security"`
	Documents       []APIDocument         `json:"documents"`
}

// APIDocument preserves one loaded contract as canonical JSON. References in
// Schema.Definition are resolved relative to the schema's Source.File.
type APIDocument struct {
	Path    string          `json:"path"`
	Content json.RawMessage `json:"content"`
}

type Tag struct {
	Name        string `json:"name"`
	Summary     string `json:"summary,omitempty"`
	Parent      string `json:"parent,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Description string `json:"description,omitempty"`
}

type Server struct {
	Name        string           `json:"name,omitempty"`
	URL         string           `json:"url"`
	Description string           `json:"description,omitempty"`
	Variables   []ServerVariable `json:"variables,omitempty"`
}

type ServerVariable struct {
	Name        string   `json:"name"`
	Default     string   `json:"default"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// Security is a disjunction of requirements; each requirement is a conjunction
// of schemes and their required scopes. Empty requirements permit anonymous use.
type SecurityRequirement map[string][]string

type SecurityScheme struct {
	ID               string          `json:"id"`
	Type             string          `json:"type"`
	Description      string          `json:"description,omitempty"`
	Name             string          `json:"name,omitempty"`
	In               string          `json:"in,omitempty"`
	Scheme           string          `json:"scheme,omitempty"`
	BearerFormat     string          `json:"bearerFormat,omitempty"`
	OpenIDConnectURL string          `json:"openIdConnectUrl,omitempty"`
	Flows            json.RawMessage `json:"flows,omitempty"`
	Raw              json.RawMessage `json:"raw,omitempty"`
}

// Operation contains effective inherited servers/security and retains the raw
// operation for protocol details that a particular reader does not yet expose.
type Operation struct {
	ID              string                `json:"id"`
	Anchor          string                `json:"anchor"`
	Method          string                `json:"method,omitempty"`
	Interaction     string                `json:"interaction,omitempty"`
	Path            string                `json:"path"`
	Service         string                `json:"service,omitempty"`
	RPCMethod       string                `json:"rpcMethod,omitempty"`
	Summary         string                `json:"summary,omitempty"`
	Description     string                `json:"description,omitempty"`
	Tags            []string              `json:"tags,omitempty"`
	Deprecated      bool                  `json:"deprecated,omitempty"`
	Servers         []Server              `json:"servers"`
	Parameters      []Parameter           `json:"parameters,omitempty"`
	RequestBody     *RequestBody          `json:"requestBody,omitempty"`
	Responses       []Response            `json:"responses,omitempty"`
	Security        []SecurityRequirement `json:"security"`
	ClientStreaming bool                  `json:"clientStreaming,omitempty"`
	ServerStreaming bool                  `json:"serverStreaming,omitempty"`
	InputSchema     string                `json:"inputSchema,omitempty"`
	OutputSchema    string                `json:"outputSchema,omitempty"`
	Source          Location              `json:"source"`
	Raw             json.RawMessage       `json:"raw,omitempty"`
}

type Parameter struct {
	Name            string          `json:"name"`
	In              string          `json:"in,omitempty"`
	Description     string          `json:"description,omitempty"`
	Required        bool            `json:"required,omitempty"`
	Deprecated      bool            `json:"deprecated,omitempty"`
	AllowReserved   bool            `json:"allowReserved,omitempty"`
	AllowEmptyValue bool            `json:"allowEmptyValue,omitempty"`
	Style           string          `json:"style,omitempty"`
	Explode         *bool           `json:"explode,omitempty"`
	Schema          string          `json:"schema,omitempty"`
	Content         []MediaType     `json:"content,omitempty"`
	Example         json.RawMessage `json:"example,omitempty"`
	ExampleJSON     string          `json:"exampleJSON,omitempty"`
	Examples        []Example       `json:"examples,omitempty"`
	Raw             json.RawMessage `json:"raw,omitempty"`
}

type RequestBody struct {
	Description string      `json:"description,omitempty"`
	Required    bool        `json:"required,omitempty"`
	Content     []MediaType `json:"content"`
}

type Response struct {
	Status      string          `json:"status"`
	Description string          `json:"description"`
	Headers     []Parameter     `json:"headers,omitempty"`
	Content     []MediaType     `json:"content,omitempty"`
	Raw         json.RawMessage `json:"raw,omitempty"`
}

type MediaType struct {
	Name        string          `json:"name"`
	Schema      string          `json:"schema,omitempty"`
	ItemSchema  string          `json:"itemSchema,omitempty"`
	Example     json.RawMessage `json:"example,omitempty"`
	ExampleJSON string          `json:"exampleJSON,omitempty"`
	Examples    []Example       `json:"examples,omitempty"`
	Encoding    json.RawMessage `json:"encoding,omitempty"`
}

type Example struct {
	Name            string          `json:"name"`
	Summary         string          `json:"summary,omitempty"`
	Description     string          `json:"description,omitempty"`
	Value           json.RawMessage `json:"value,omitempty"`
	ValueJSON       string          `json:"valueJSON,omitempty"`
	ExternalValue   string          `json:"externalValue,omitempty"`
	DataValue       json.RawMessage `json:"dataValue,omitempty"`
	SerializedValue *string         `json:"serializedValue,omitempty"`
	Raw             json.RawMessage `json:"raw,omitempty"`
}

// Schema is a stable node; Definition retains composition, dialect and recursive
// references rather than expanding them into an unbounded object tree.
type Schema struct {
	ID          string            `json:"id"`
	Anchor      string            `json:"anchor"`
	Name        string            `json:"name"`
	Definition  json.RawMessage   `json:"definition"`
	ExampleJSON string            `json:"exampleJSON,omitempty"`
	DefaultJSON string            `json:"defaultJSON,omitempty"`
	References  []SchemaReference `json:"references,omitempty"`
	Source      Location          `json:"source"`
}

// SchemaReference identifies a reference within a schema without expanding it.
// Dynamic references retain their static fallback; readers must not treat that
// fallback as a complete evaluation of JSON Schema dynamic scope.
type SchemaReference struct {
	Pointer   string `json:"pointer"`
	Reference string `json:"reference"`
	Target    string `json:"target"`
	Dynamic   bool   `json:"dynamic,omitempty"`
}

// APIAnchor gives operations and schemas deterministic safe fragment identifiers.
func APIAnchor(kind, id string) string {
	digest := sha256.Sum256([]byte(id))
	return kind + "-" + hex.EncodeToString(digest[:16])
}
