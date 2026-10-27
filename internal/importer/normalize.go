package importer

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/kordax/pb-md5-generator/document"
)

func (n *openAPI) normalize() error {
	root := object(n.root.value)
	if dialect := text(root["jsonSchemaDialect"]); dialect != "" && !supportedDialect(dialect) {
		return n.sources.fail(n.root.name, "/jsonSchemaDialect", "api.dialect", "unsupported default JSON Schema dialect")
	}
	n.api.Servers = normalizeServers(root["servers"])
	if len(n.api.Servers) == 0 {
		n.api.Servers = []document.Server{{URL: "/"}}
	}
	components := object(root["components"])
	security := object(components["securitySchemes"])
	for _, name := range sortedKeys(security) {
		target, value, err := n.referenceObject(n.root.name, "/components/securitySchemes/"+escapePointer(name), security[name])
		if err != nil {
			return err
		}
		if err := n.validator.validateObject("securitySchemes", value, n.version); err != nil {
			return n.sources.fail(target.file, target.pointer, "api.structure", "invalid security scheme")
		}
		n.api.SecuritySchemes = append(n.api.SecuritySchemes, normalizeSecurityScheme(name, value))
	}
	var err error
	n.api.Security, err = n.securityRequirements(n.root.name, "/security", root["security"])
	if err != nil {
		return err
	}
	seenTags := make(map[string]bool)
	for _, item := range array(root["tags"]) {
		tag := object(item)
		name := text(tag["name"])
		if seenTags[name] {
			return n.sources.fail(n.root.name, "/tags", "api.structure", "duplicate tag name")
		}
		seenTags[name] = true
		n.api.Tags = append(n.api.Tags, document.Tag{Name: name, Description: text(tag["description"]), Summary: text(tag["summary"]), Parent: text(tag["parent"]), Kind: text(tag["kind"])})
	}
	if err := n.scanReferences(n.root.name, "", n.root.value, make(map[referenceTarget]bool), 0); err != nil {
		return err
	}
	ids := make(map[string]bool)
	visited := make(map[string]bool)
	pathShapes := make(map[string]bool)
	for _, section := range []string{"paths", "webhooks"} {
		for _, name := range sortedKeys(object(root[section])) {
			if strings.HasPrefix(name, "x-") {
				continue
			}
			interaction := "request"
			if section == "webhooks" {
				interaction = "webhook"
			}
			if interaction == "request" {
				if !strings.HasPrefix(name, "/") || strings.ContainsAny(name, "?#") {
					return n.sources.fail(n.root.name, "/paths/"+escapePointer(name), "api.operation", "invalid API path")
				}
				shape := pathVariable.ReplaceAllString(name, "{}")
				if pathShapes[shape] {
					return n.sources.fail(n.root.name, "/paths/"+escapePointer(name), "api.operation", "equivalent templated paths are not allowed")
				}
				pathShapes[shape] = true
			}
			if err := n.pathItem(n.root.name, "/"+section+"/"+escapePointer(name), name, object(root[section])[name], interaction, ids, visited, 0); err != nil {
				return err
			}
		}
	}
	return nil
}

func supportedDialect(dialect string) bool {
	return dialect == "https://spec.openapis.org/oas/3.1/dialect/base" || dialect == "https://json-schema.org/draft/2020-12/schema"
}

func normalizeServers(value any) []document.Server {
	servers := []document.Server{}
	for _, item := range array(value) {
		definition := object(item)
		server := document.Server{Name: text(definition["name"]), URL: text(definition["url"]), Description: text(definition["description"])}
		variables := object(definition["variables"])
		for _, name := range sortedKeys(variables) {
			variable := object(variables[name])
			server.Variables = append(server.Variables, document.ServerVariable{Name: name, Default: text(variable["default"]), Description: text(variable["description"]), Enum: stringArray(variable["enum"])})
		}
		servers = append(servers, server)
	}
	return servers
}

func normalizeSecurityScheme(id string, value map[string]any) document.SecurityScheme {
	return document.SecurityScheme{ID: id, Type: text(value["type"]), Description: text(value["description"]), Name: text(value["name"]), In: text(value["in"]), Scheme: text(value["scheme"]), BearerFormat: text(value["bearerFormat"]), OpenIDConnectURL: text(value["openIdConnectUrl"]), Flows: rawPresent(value, "flows"), Raw: raw(value)}
}

func (n *openAPI) securityRequirements(file, pointer string, value any) ([]document.SecurityRequirement, error) {
	requirements := []document.SecurityRequirement{}
	for _, item := range array(value) {
		requirement := make(document.SecurityRequirement)
		for _, name := range sortedKeys(object(item)) {
			known := false
			for _, scheme := range n.api.SecuritySchemes {
				if scheme.ID == name {
					known = true
					break
				}
			}
			if !known {
				if strings.HasPrefix(n.version, "3.2.") && strings.ContainsAny(name, "#:/.") {
					target, definition, err := n.referenceObject(file, pointer, map[string]any{"$ref": name})
					if err != nil {
						return nil, err
					}
					if err := n.validator.validateObject("securitySchemes", definition, n.version); err != nil {
						return nil, n.sources.fail(target.file, target.pointer, "api.security", "invalid referenced security scheme")
					}
					n.api.SecuritySchemes = append(n.api.SecuritySchemes, normalizeSecurityScheme(name, definition))
				} else {
					return nil, n.sources.fail(file, pointer, "api.security", "security requirement names an unknown scheme")
				}
			}
			requirement[name] = stringArray(object(item)[name])
		}
		requirements = append(requirements, requirement)
	}
	return requirements, nil
}

func (n *openAPI) pathItem(file, pointer, name string, value any, interaction string, ids, visited map[string]bool, depth int) error {
	if depth > MaxDepth {
		return n.sources.fail(file, pointer, "api.limit", "callback nesting exceeds depth budget")
	}
	original := object(value)
	if _, present := original["$ref"]; present {
		for key := range original {
			if key != "$ref" && key != "summary" && key != "description" {
				return n.sources.fail(file, pointer, "api.reference", "path-item reference siblings need an unambiguous standalone path item")
			}
		}
	}
	target, item, err := n.referenceObject(file, pointer, value)
	if err != nil {
		return err
	}
	if err := n.validator.validatePathItem(item, n.version); err != nil {
		return n.sources.fail(target.file, target.pointer, "api.structure", "invalid referenced path item")
	}
	key := target.file + "#" + target.pointer + "|" + interaction + "|" + name
	if visited[key] {
		return nil
	}
	visited[key] = true
	servers := n.api.Servers
	if own, present := item["servers"]; present {
		servers = normalizeServers(own)
	}
	parameters, err := n.parameters(target.file, target.pointer+"/parameters", item["parameters"])
	if err != nil {
		return err
	}
	methods := map[string]any{}
	for _, method := range []string{"get", "put", "post", "delete", "options", "head", "patch", "trace", "query"} {
		if operation, present := item[method]; present {
			methods[strings.ToUpper(method)] = operation
		}
	}
	for method, operation := range object(item["additionalOperations"]) {
		if _, duplicate := methods[method]; duplicate || !validMethod.MatchString(method) {
			return n.sources.fail(target.file, target.pointer, "api.operation", "invalid or duplicate additional HTTP method")
		}
		methods[method] = operation
	}
	for _, method := range sortedKeys(methods) {
		opPointer := target.pointer + "/" + strings.ToLower(method)
		if _, extra := object(item["additionalOperations"])[method]; extra {
			opPointer = target.pointer + "/additionalOperations/" + escapePointer(method)
		}
		operation := object(methods[method])
		id := text(operation["operationId"])
		if id == "" {
			id = document.APIAnchor("operation", target.file+"#"+opPointer+"|"+interaction+"|"+name)
		}
		if ids[id] {
			return n.sources.fail(target.file, opPointer, "api.operation", "duplicate operation identifier")
		}
		ids[id] = true
		if len(n.api.Operations) >= MaxOperations {
			return n.sources.fail(target.file, opPointer, "api.limit", "API operation count exceeded")
		}
		effectiveServers := servers
		if own, present := operation["servers"]; present {
			effectiveServers = normalizeServers(own)
		}
		effectiveSecurity := n.api.Security
		if own, present := operation["security"]; present {
			effectiveSecurity, err = n.securityRequirements(target.file, opPointer+"/security", own)
			if err != nil {
				return err
			}
		}
		own, err := n.parameters(target.file, opPointer+"/parameters", operation["parameters"])
		if err != nil {
			return err
		}
		effectiveParameters := mergeParameters(parameters, own)
		query, querystring := false, 0
		for _, parameter := range effectiveParameters {
			query = query || parameter.In == "query"
			if parameter.In == "querystring" {
				querystring++
			}
		}
		if querystring > 1 || query && querystring > 0 {
			return n.sources.fail(target.file, opPointer, "api.parameter", "query and querystring parameters cannot be combined")
		}
		if interaction == "request" {
			if err := n.validatePathParameters(target.file, opPointer, name, effectiveParameters); err != nil {
				return err
			}
		}
		body, err := n.requestBody(target.file, opPointer+"/requestBody", operation["requestBody"])
		if err != nil {
			return err
		}
		responses, err := n.responses(target.file, opPointer+"/responses", operation["responses"])
		if err != nil {
			return err
		}
		n.api.Operations = append(n.api.Operations, document.Operation{ID: id, Anchor: document.APIAnchor("operation", id), Method: method, Interaction: interaction, Path: name, Summary: text(operation["summary"]), Description: text(operation["description"]), Tags: stringArray(operation["tags"]), Deprecated: boolean(operation["deprecated"]), Servers: effectiveServers, Security: effectiveSecurity, Parameters: effectiveParameters, RequestBody: body, Responses: responses, Raw: raw(operation), Source: sourceLocation(n.sources.files[target.file], opPointer)})
		callbacks := object(operation["callbacks"])
		for _, callbackName := range sortedKeys(callbacks) {
			callbackTarget, callback, err := n.referenceObject(target.file, opPointer+"/callbacks/"+escapePointer(callbackName), callbacks[callbackName])
			if err != nil {
				return err
			}
			for _, expression := range sortedKeys(callback) {
				if strings.HasPrefix(expression, "x-") {
					continue
				}
				if err := n.pathItem(callbackTarget.file, callbackTarget.pointer+"/"+escapePointer(expression), expression, callback[expression], "callback", ids, visited, depth+1); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

var validMethod = regexp.MustCompile(`^[!#$%&'*+.^_\x60|~0-9A-Za-z-]+$`)
var pathVariable = regexp.MustCompile(`\{([^{}]+)\}`)

func (n *openAPI) validatePathParameters(file, pointer, path string, parameters []document.Parameter) error {
	needed := map[string]bool{}
	for _, match := range pathVariable.FindAllStringSubmatch(path, -1) {
		needed[match[1]] = true
	}
	cleaned := pathVariable.ReplaceAllString(path, "")
	if strings.ContainsAny(cleaned, "{}") {
		return n.sources.fail(file, pointer, "api.operation", "malformed path template")
	}
	for _, parameter := range parameters {
		if parameter.In == "path" {
			if !needed[parameter.Name] {
				return n.sources.fail(file, pointer, "api.operation", "path parameter is absent from the path template")
			}
			delete(needed, parameter.Name)
		}
	}
	if len(needed) != 0 {
		return n.sources.fail(file, pointer, "api.operation", "path template has an undeclared parameter")
	}
	return nil
}

func parameterKey(parameter document.Parameter) string {
	name := parameter.Name
	if parameter.In == "header" {
		name = strings.ToLower(name)
	}
	return parameter.In + "\x00" + name
}

func mergeParameters(inherited, own []document.Parameter) []document.Parameter {
	result := append([]document.Parameter{}, inherited...)
	indices := make(map[string]int, len(result))
	for index, parameter := range result {
		indices[parameterKey(parameter)] = index
	}
	for _, parameter := range own {
		if index, found := indices[parameterKey(parameter)]; found {
			result[index] = parameter
		} else {
			indices[parameterKey(parameter)] = len(result)
			result = append(result, parameter)
		}
	}
	return result
}

func (n *openAPI) parameters(file, pointer string, value any) ([]document.Parameter, error) {
	result := []document.Parameter{}
	seen := map[string]bool{}
	for index, item := range array(value) {
		target, definition, err := n.referenceObject(file, fmt.Sprintf("%s/%d", pointer, index), item)
		if err != nil {
			return nil, err
		}
		if err := n.validator.validateObject("parameters", definition, n.version); err != nil {
			return nil, n.sources.fail(target.file, target.pointer, "api.structure", "invalid referenced parameter")
		}
		if strings.EqualFold(text(definition["in"]), "header") {
			name := text(definition["name"])
			if strings.EqualFold(name, "Accept") || strings.EqualFold(name, "Content-Type") || strings.EqualFold(name, "Authorization") {
				continue
			}
		}
		parameter, err := n.parameter(target.file, target.pointer, definition)
		if err != nil {
			return nil, err
		}
		key := parameterKey(parameter)
		if seen[key] {
			return nil, n.sources.fail(target.file, target.pointer, "api.operation", "duplicate parameter name and location")
		}
		seen[key] = true
		result = append(result, parameter)
	}
	querystring, query := 0, false
	for _, parameter := range result {
		if parameter.In == "querystring" {
			querystring++
		}
		if parameter.In == "query" {
			query = true
		}
	}
	if querystring > 1 || querystring > 0 && query {
		return nil, n.sources.fail(file, pointer, "api.operation", "querystring and query parameters cannot be combined")
	}
	return result, nil
}

func (n *openAPI) parameter(file, pointer string, value map[string]any) (document.Parameter, error) {
	parameter := document.Parameter{Name: text(value["name"]), In: text(value["in"]), Description: text(value["description"]), Required: boolean(value["required"]), Deprecated: boolean(value["deprecated"]), AllowReserved: boolean(value["allowReserved"]), AllowEmptyValue: boolean(value["allowEmptyValue"]), Style: text(value["style"]), Example: rawPresent(value, "example"), Raw: raw(value)}
	if explode, present := value["explode"].(bool); present {
		parameter.Explode = &explode
	}
	if schema, present := value["schema"]; present {
		id, err := n.addSchema(file, pointer+"/schema", schema, parameter.Name)
		if err != nil {
			return parameter, err
		}
		parameter.Schema = id
	}
	var err error
	parameter.Content, err = n.content(file, pointer+"/content", value["content"])
	if err != nil {
		return parameter, err
	}
	parameter.Examples, err = n.examples(file, pointer+"/examples", value["examples"])
	if parameter.Schema != "" && len(parameter.Content) == 0 {
		if parameter.Style == "" {
			switch parameter.In {
			case "query", "cookie":
				parameter.Style = "form"
			default:
				parameter.Style = "simple"
			}
		}
		if parameter.Explode == nil {
			explode := parameter.Style == "form"
			parameter.Explode = &explode
		}
	}
	return parameter, err
}

func (n *openAPI) requestBody(file, pointer string, value any) (*document.RequestBody, error) {
	if value == nil {
		return nil, nil
	}
	target, body, err := n.referenceObject(file, pointer, value)
	if err != nil {
		return nil, err
	}
	if err := n.validator.validateObject("requestBodies", body, n.version); err != nil {
		return nil, n.sources.fail(target.file, target.pointer, "api.structure", "invalid referenced request body")
	}
	content, err := n.content(target.file, target.pointer+"/content", body["content"])
	if err != nil {
		return nil, err
	}
	return &document.RequestBody{Description: text(body["description"]), Required: boolean(body["required"]), Content: content}, nil
}

func (n *openAPI) responses(file, pointer string, value any) ([]document.Response, error) {
	result := []document.Response{}
	responses := object(value)
	for _, status := range sortedKeys(responses) {
		if strings.HasPrefix(status, "x-") {
			continue
		}
		target, response, err := n.referenceObject(file, pointer+"/"+escapePointer(status), responses[status])
		if err != nil {
			return nil, err
		}
		if err := n.validator.validateObject("responses", response, n.version); err != nil {
			return nil, n.sources.fail(target.file, target.pointer, "api.structure", "invalid referenced response")
		}
		content, err := n.content(target.file, target.pointer+"/content", response["content"])
		if err != nil {
			return nil, err
		}
		headers := []document.Parameter{}
		for _, name := range sortedKeys(object(response["headers"])) {
			if strings.EqualFold(name, "Content-Type") {
				continue
			}
			headerTarget, header, err := n.referenceObject(target.file, target.pointer+"/headers/"+escapePointer(name), object(response["headers"])[name])
			if err != nil {
				return nil, err
			}
			if err := n.validator.validateObject("headers", header, n.version); err != nil {
				return nil, n.sources.fail(headerTarget.file, headerTarget.pointer, "api.structure", "invalid referenced header")
			}
			parameter, err := n.parameter(headerTarget.file, headerTarget.pointer, header)
			if err != nil {
				return nil, err
			}
			parameter.Name = name
			parameter.In = "header"
			headers = append(headers, parameter)
		}
		result = append(result, document.Response{Status: status, Description: text(response["description"]), Content: content, Headers: headers, Raw: raw(response)})
	}
	return result, nil
}

func (n *openAPI) content(file, pointer string, value any) ([]document.MediaType, error) {
	result := []document.MediaType{}
	content := object(value)
	for _, name := range sortedKeys(content) {
		target, media, err := n.referenceObject(file, pointer+"/"+escapePointer(name), content[name])
		if err != nil {
			return nil, err
		}
		entry := document.MediaType{Name: name, Example: rawPresent(media, "example"), Encoding: rawPresent(media, "encoding")}
		if schema, present := media["schema"]; present {
			id, err := n.addSchema(target.file, target.pointer+"/schema", schema, name)
			if err != nil {
				return nil, err
			}
			entry.Schema = id
		}
		if schema, present := media["itemSchema"]; present {
			id, err := n.addSchema(target.file, target.pointer+"/itemSchema", schema, name+" item")
			if err != nil {
				return nil, err
			}
			entry.ItemSchema = id
		}
		entry.Examples, err = n.examples(target.file, target.pointer+"/examples", media["examples"])
		if err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, nil
}

func (n *openAPI) examples(file, pointer string, value any) ([]document.Example, error) {
	result := []document.Example{}
	examples := object(value)
	for _, name := range sortedKeys(examples) {
		target, example, err := n.referenceObject(file, pointer+"/"+escapePointer(name), examples[name])
		if err != nil {
			return nil, err
		}
		if err := n.validator.validateObject("examples", example, n.version); err != nil {
			return nil, n.sources.fail(target.file, target.pointer, "api.structure", "invalid referenced example")
		}
		entry := document.Example{Name: name, Summary: text(example["summary"]), Description: text(example["description"]), Value: rawPresent(example, "value"), DataValue: rawPresent(example, "dataValue"), ExternalValue: text(example["externalValue"]), Raw: raw(example)}
		if wire, present := example["serializedValue"].(string); present {
			entry.SerializedValue = &wire
		}
		result = append(result, entry)
	}
	return result, nil
}

func rawPresent(value map[string]any, key string) []byte {
	if item, present := value[key]; present {
		return raw(item)
	}
	return nil
}

func stringArray(value any) []string {
	result := []string{}
	for _, item := range array(value) {
		result = append(result, text(item))
	}
	return result
}
