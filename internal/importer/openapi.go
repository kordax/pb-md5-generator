package importer

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/kordax/pb-md5-generator/document"
)

type referenceTarget struct {
	file    string
	pointer string
}

type openAPI struct {
	sources       *sources
	root          *parsedSource
	version       string
	api           *document.API
	schemas       map[string]document.Schema
	visited       map[string]bool
	resources     map[string]referenceTarget
	bases         map[string]string
	indexed       map[string]bool
	schemaIndexed map[string]bool
	edges         map[string][]document.SchemaReference
	rawBytes      int
	resolving     int
	validator     *contractValidator
}

var openAPIVersion = regexp.MustCompile(`^3\.[012]\.(0|[1-9][0-9]*)$`)

// OpenAPI imports supported OpenAPI 3.0, 3.1 and 3.2 feature sets. It validates
// the declared version, keeps schema references as graph edges, and never lets
// validator or reference resolution fall back to filesystem or network access.
func OpenAPI(ctx context.Context, name string, data []byte, load Load) (*document.API, error) {
	sources := newSources(ctx, load)
	root, err := sources.read(name, data)
	if err != nil {
		return nil, err
	}
	value := object(root.value)
	version := text(value["openapi"])
	if !openAPIVersion.MatchString(version) {
		return nil, sources.fail(name, "/openapi", "api.version", "supported OpenAPI feature sets are 3.0, 3.1 and 3.2")
	}
	validator, err := newContractValidator(version)
	if err != nil {
		return nil, sources.fail(name, "", "api.validation", "cannot initialize embedded OpenAPI validation")
	}
	if pointer, err := validator.validateDocument(value); err != nil {
		return nil, sources.fail(name, pointer, "api.structure", "document does not match its declared OpenAPI version")
	}
	info := object(value["info"])
	api := &document.API{
		ID: name, Protocol: "http", Format: "openapi", Version: version,
		APIVersion: text(info["version"]), Title: text(info["title"]), Description: text(info["description"]),
		Source: sourceLocation(root, ""), Operations: []document.Operation{}, Schemas: []document.Schema{},
		Security: []document.SecurityRequirement{}, Documents: []document.APIDocument{},
	}
	normalizer := &openAPI{sources: sources, root: root, version: version, api: api, validator: validator,
		schemas: make(map[string]document.Schema), visited: make(map[string]bool), resources: make(map[string]referenceTarget), bases: make(map[string]string), indexed: make(map[string]bool), edges: make(map[string][]document.SchemaReference)}
	if err := normalizer.indexSource(root, false); err != nil {
		return nil, err
	}
	if err := normalizer.normalize(); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(normalizer.schemas))
	for id := range normalizer.schemas {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		api.Schemas = append(api.Schemas, normalizer.schemas[id])
	}
	for _, name := range sortedSourceNames(sources.files) {
		source := sources.files[name]
		api.Documents = append(api.Documents, document.APIDocument{Path: name, Content: source.raw})
	}
	preserveExampleJSON(api)
	if err := checkModelSize(api); err != nil {
		return nil, sources.fail(name, "", "api.limit", "normalized API model exceeds its byte or node budget")
	}
	return api, nil
}

func sortedSourceNames(files map[string]*parsedSource) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (n *openAPI) indexSource(source *parsedSource, schema bool) error {
	if n.indexed[source.name] {
		return nil
	}
	n.indexed[source.name] = true
	n.resources[source.name] = referenceTarget{file: source.name}
	value := object(source.value)
	base := source.name
	if self := text(value["$self"]); self != "" {
		parsed, err := referenceURL(self)
		if err != nil || parsed.Fragment != "" {
			return n.sources.fail(source.name, "/$self", "api.reference", "invalid document base URI")
		}
		base = parsed.String()
		n.resources[base] = referenceTarget{file: source.name}
	}
	n.bases[source.name+"#"] = base
	if declared, isContract := value["openapi"]; isContract {
		version := text(declared)
		if !openAPIVersion.MatchString(version) || strings.Split(version, ".")[1] != strings.Split(n.version, ".")[1] {
			return n.sources.fail(source.name, "/openapi", "api.version", "referenced OpenAPI documents must use the root feature set")
		}
		if pointer, err := n.validator.validateDocument(value); err != nil {
			return n.sources.fail(source.name, pointer, "api.structure", "referenced document does not match its declared OpenAPI version")
		}
		if dialect := text(value["jsonSchemaDialect"]); dialect != "" && !supportedDialect(dialect) {
			return n.sources.fail(source.name, "/jsonSchemaDialect", "api.dialect", "unsupported referenced JSON Schema dialect")
		}
		components := object(object(value["components"])["schemas"])
		for _, name := range sortedKeys(components) {
			if err := n.indexSchema(source.name, "/components/schemas/"+escapePointer(name), components[name], base, 0); err != nil {
				return err
			}
		}
		return nil
	}
	if schema {
		return n.indexSchema(source.name, "", source.value, base, 0)
	}
	return nil
}

func (n *openAPI) indexSchema(file, pointer string, value any, base string, depth int) error {
	if depth > MaxDepth {
		return n.sources.fail(file, pointer, "api.limit", "schema nesting exceeds its depth budget")
	}
	if err := n.sources.ctx.Err(); err != nil {
		return err
	}
	key := file + "#" + pointer
	if n.schemaIndexed[key] {
		return nil
	}
	if n.schemaIndexed == nil {
		n.schemaIndexed = make(map[string]bool)
	}
	n.schemaIndexed[key] = true
	mapping := object(value)
	if dialect := text(mapping["$schema"]); dialect != "" && !supportedDialect(dialect) {
		return n.sources.fail(file, pointer, "api.dialect", "unsupported JSON Schema dialect")
	}
	for vocabulary, required := range object(mapping["$vocabulary"]) {
		if boolean(required) && !knownVocabulary(vocabulary) {
			return n.sources.fail(file, pointer, "api.dialect", "unsupported required JSON Schema vocabulary")
		}
	}
	if id := text(mapping["$id"]); id != "" {
		parsed, err := referenceURL(id)
		if err != nil || parsed.Fragment != "" {
			return n.sources.fail(file, pointer, "api.reference", "invalid schema resource identifier")
		}
		parent, _ := url.Parse(base)
		base = resolveLogicalURI(parent, parsed).String()
		if err := n.resource(base, referenceTarget{file: file, pointer: pointer}); err != nil {
			return err
		}
	}
	n.bases[file+"#"+pointer] = base
	for _, keyword := range []string{"$anchor", "$dynamicAnchor"} {
		if anchor := text(mapping[keyword]); anchor != "" {
			if err := n.resource(base+"#"+anchor, referenceTarget{file: file, pointer: pointer}); err != nil {
				return err
			}
		}
	}
	return schemaChildren(mapping, pointer, func(pointer string, child any) error {
		return n.indexSchema(file, pointer, child, base, depth+1)
	})
}

func (n *openAPI) resource(id string, target referenceTarget) error {
	if previous, exists := n.resources[id]; exists && previous != target {
		return n.sources.fail(target.file, target.pointer, "api.reference", "duplicate schema resource or anchor identifier")
	}
	n.resources[id] = target
	return nil
}

func schemaChildren(mapping map[string]any, pointer string, visit func(string, any) error) error {
	for _, keyword := range []string{"$defs", "definitions", "properties", "patternProperties", "dependentSchemas"} {
		children := object(mapping[keyword])
		for _, key := range sortedKeys(children) {
			if err := visit(pointer+"/"+keyword+"/"+escapePointer(key), children[key]); err != nil {
				return err
			}
		}
	}
	for _, keyword := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		for i, child := range array(mapping[keyword]) {
			if err := visit(pointer+"/"+keyword+"/"+strconv.Itoa(i), child); err != nil {
				return err
			}
		}
	}
	for _, keyword := range []string{"not", "if", "then", "else", "items", "additionalItems", "additionalProperties", "unevaluatedProperties", "unevaluatedItems", "contains", "propertyNames", "contentSchema"} {
		if child, exists := mapping[keyword]; exists {
			if values, list := child.([]any); list {
				for i, value := range values {
					if err := visit(pointer+"/"+keyword+"/"+strconv.Itoa(i), value); err != nil {
						return err
					}
				}
			} else if err := visit(pointer+"/"+keyword, child); err != nil {
				return err
			}
		}
	}
	return nil
}

func (n *openAPI) resolve(file, pointer, ref, base string, schema bool) (referenceTarget, any, error) {
	n.sources.refs++
	if n.sources.refs > MaxReferences {
		return referenceTarget{}, nil, n.sources.fail(file, pointer, "api.limit", "API reference budget exceeded")
	}
	parsed, err := referenceURL(ref)
	if err != nil || strings.Contains(parsed.Path, "\\") {
		return referenceTarget{}, nil, n.sources.fail(file, pointer, "api.reference", "invalid reference URI")
	}
	parent, _ := url.Parse(base)
	if !parsed.IsAbs() && parsed.Host == "" && !parent.IsAbs() && parsed.Path != "" {
		candidate := path.Join(path.Dir(parent.Path), parsed.Path)
		if strings.HasPrefix(parsed.Path, "/") || !validSourceName(candidate) {
			return referenceTarget{}, nil, n.sources.fail(file, pointer, "api.reference", "reference escapes the project")
		}
	}
	resolved := resolveLogicalURI(parent, parsed)
	if target, exists := n.resources[resolved.String()]; exists {
		value, err := n.at(target)
		return target, value, err
	}
	fragment := resolved.Fragment
	resolved.Fragment, resolved.RawFragment = "", ""
	resource := resolved.String()
	target, exists := n.resources[resource]
	if !exists {
		if resolved.IsAbs() || resolved.Host != "" || !validSourceName(resolved.Path) {
			return referenceTarget{}, nil, n.sources.fail(file, pointer, "api.reference", "remote or escaping references are disabled")
		}
		loaded, err := n.sources.read(resolved.Path, nil)
		if err != nil {
			return referenceTarget{}, nil, err
		}
		if err := n.indexSource(loaded, schema); err != nil {
			return referenceTarget{}, nil, err
		}
		target = referenceTarget{file: loaded.name}
	}
	if fragment != "" {
		if strings.HasPrefix(fragment, "/") {
			target.pointer += fragment
		} else if anchored, exists := n.resources[resource+"#"+fragment]; exists {
			target = anchored
		} else if anchored, exists := n.resources[n.bases[target.file+"#"+target.pointer]+"#"+fragment]; exists {
			target = anchored
		} else {
			return referenceTarget{}, nil, n.sources.fail(file, pointer, "api.reference", "reference anchor does not exist")
		}
	}
	value, err := n.at(target)
	return target, value, err
}

// net/url adds a leading slash when resolving relative base paths. Contract
// names instead live in a project-relative namespace supplied by the caller.
func resolveLogicalURI(parent, reference *url.URL) *url.URL {
	if !parent.IsAbs() && parent.Host == "" && !strings.HasPrefix(parent.Path, "/") && !reference.IsAbs() && reference.Host == "" && !strings.HasPrefix(reference.Path, "/") {
		resolved := &url.URL{Path: parent.Path, Fragment: reference.Fragment}
		if reference.Path != "" {
			resolved.Path = path.Join(path.Dir(parent.Path), reference.Path)
		}
		return resolved
	}
	return parent.ResolveReference(reference)
}

func knownVocabulary(vocabulary string) bool {
	for _, name := range []string{"core", "applicator", "unevaluated", "validation", "meta-data", "format-annotation", "format-assertion", "content"} {
		if vocabulary == "https://json-schema.org/draft/2020-12/vocab/"+name {
			return true
		}
	}
	return vocabulary == "https://spec.openapis.org/oas/3.1/vocab/base"
}

func (n *openAPI) at(target referenceTarget) (any, error) {
	source := n.sources.files[target.file]
	if source == nil {
		return nil, n.sources.fail(target.file, target.pointer, "api.reference", "referenced document is unavailable")
	}
	value := source.value
	if target.pointer == "" {
		return value, nil
	}
	for _, encoded := range strings.Split(strings.TrimPrefix(target.pointer, "/"), "/") {
		key, err := unescapePointer(encoded)
		if err != nil {
			return nil, n.sources.fail(target.file, target.pointer, "api.reference", "invalid JSON pointer escape")
		}
		switch object := value.(type) {
		case map[string]any:
			var found bool
			value, found = object[key]
			if !found {
				return nil, n.sources.fail(target.file, target.pointer, "api.reference", "reference target does not exist")
			}
		case []any:
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || index >= len(object) || strconv.Itoa(index) != key {
				return nil, n.sources.fail(target.file, target.pointer, "api.reference", "invalid array reference target")
			}
			value = object[index]
		default:
			return nil, n.sources.fail(target.file, target.pointer, "api.reference", "reference traverses a scalar value")
		}
	}
	return value, nil
}

func unescapePointer(value string) (string, error) {
	var result strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '~' {
			result.WriteByte(value[i])
			continue
		}
		i++
		if i >= len(value) || (value[i] != '0' && value[i] != '1') {
			return "", fmt.Errorf("invalid pointer escape")
		}
		if value[i] == '0' {
			result.WriteByte('~')
		} else {
			result.WriteByte('/')
		}
	}
	return result.String(), nil
}

func (n *openAPI) referenceObject(file, pointer string, value any) (referenceTarget, map[string]any, error) {
	target := referenceTarget{file: file, pointer: pointer}
	seen := make(map[referenceTarget]bool)
	for depth := 0; depth <= MaxDepth; depth++ {
		mapping := object(value)
		if mapping == nil {
			return target, nil, n.sources.fail(target.file, target.pointer, "api.structure", "expected an object")
		}
		refValue, present := mapping["$ref"]
		if !present {
			return target, mapping, nil
		}
		ref := text(refValue)
		if seen[target] {
			return target, nil, n.sources.fail(target.file, target.pointer, "api.reference", "cyclic non-schema reference")
		}
		seen[target] = true
		base := n.bases[target.file+"#"]
		var err error
		target, value, err = n.resolve(target.file, target.pointer, ref, base, false)
		if err != nil {
			return target, nil, err
		}
		if !strings.HasPrefix(n.version, "3.0.") {
			resolved := object(value)
			copy := make(map[string]any, len(resolved)+2)
			for key, value := range resolved {
				copy[key] = value
			}
			for _, key := range []string{"summary", "description"} {
				if value, present := mapping[key]; present {
					copy[key] = value
				}
			}
			value = copy
		}
	}
	return target, nil, n.sources.fail(file, pointer, "api.limit", "reference chain exceeds depth budget")
}

func (n *openAPI) addSchema(file, pointer string, value any, name string) (string, error) {
	id := file + "#" + pointer
	if n.visited[id] {
		return id, nil
	}
	if len(n.schemas) >= MaxSchemas {
		return "", n.sources.fail(file, pointer, "api.limit", "API schema count exceeded")
	}
	if object(value) == nil {
		if _, boolean := value.(bool); !boolean || strings.HasPrefix(n.version, "3.0.") {
			return "", n.sources.fail(file, pointer, "api.structure", "invalid schema object")
		}
	}
	if n.resolving >= MaxDepth {
		return "", n.sources.fail(file, pointer, "api.limit", "schema reference chain exceeds depth budget")
	}
	n.resolving++
	defer func() { n.resolving-- }()
	n.visited[id] = true
	base := n.bases[file+"#"]
	if err := n.indexSchema(file, pointer, value, base, 0); err != nil {
		return "", err
	}
	if err := n.validator.validateSchema(value, n.version); err != nil {
		return "", n.sources.fail(file, pointer, "api.dialect", "invalid or unsupported schema dialect/keywords")
	}
	definition := raw(value)
	n.rawBytes += len(definition)
	if n.rawBytes > MaxModelBytes {
		return "", n.sources.fail(file, pointer, "api.limit", "API schema byte budget exceeded")
	}
	if name == "" {
		name = path.Base(pointer)
		if name == "." || name == "/" {
			name = path.Base(file)
		}
	}
	n.schemas[id] = document.Schema{ID: id, Anchor: document.APIAnchor("schema", id), Name: name, Definition: definition, Source: sourceLocation(n.sources.files[file], pointer)}
	if err := n.schemaReferences(file, pointer, value, 0, id); err != nil {
		return "", err
	}
	definitionNode := n.schemas[id]
	definitionNode.References = n.edges[id]
	n.schemas[id] = definitionNode
	return id, nil
}

func (n *openAPI) schemaReferences(file, pointer string, value any, depth int, owner string) error {
	if depth > MaxDepth {
		return n.sources.fail(file, pointer, "api.limit", "schema reference nesting exceeded")
	}
	mapping := object(value)
	base := n.bases[file+"#"+pointer]
	for _, keyword := range []string{"$ref", "$dynamicRef"} {
		if reference, present := mapping[keyword]; present {
			ref := text(reference)
			target, value, err := n.resolve(file, pointer, ref, base, true)
			if err != nil {
				return err
			}
			rootPointer := strings.SplitN(owner, "#", 2)[1]
			n.edges[owner] = append(n.edges[owner], document.SchemaReference{Pointer: strings.TrimPrefix(pointer, rootPointer) + "/" + keyword, Reference: ref, Target: target.file + "#" + target.pointer, Dynamic: keyword != "$ref"})
			if _, err := n.addSchema(target.file, target.pointer, value, ""); err != nil {
				return err
			}
		}
	}
	return schemaChildren(mapping, pointer, func(childPointer string, child any) error {
		return n.schemaReferences(file, childPointer, child, depth+1, owner)
	})
}
