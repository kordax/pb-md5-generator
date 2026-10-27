package importer

import "strconv"

// Scan only contract-defined object positions. User data (examples, defaults,
// OAuth scope names and extension payloads) must never become reference syntax.
func (n *openAPI) scanReferences(file, pointer string, value any, seen map[referenceTarget]bool, depth int) error {
	return n.scanTyped(file, pointer, value, "document", seen, depth)
}

func (n *openAPI) scanTyped(file, pointer string, value any, kind string, seen map[referenceTarget]bool, depth int) error {
	if err := n.sources.ctx.Err(); err != nil {
		return err
	}
	if depth > MaxDepth {
		return n.sources.fail(file, pointer, "api.limit", "reference graph depth exceeded")
	}
	key := referenceTarget{file: file, pointer: pointer + "|" + kind}
	if seen[key] {
		return nil
	}
	seen[key] = true
	if kind == "schema" {
		_, err := n.addSchema(file, pointer, value, "")
		return err
	}
	mapping := object(value)
	if kind != "document" {
		target, resolved, err := n.referenceObject(file, pointer, value)
		if err != nil {
			return err
		}
		if target.file != file || target.pointer != pointer {
			return n.scanTyped(target.file, target.pointer, resolved, kind, seen, depth+1)
		}
		mapping = resolved
		if kind == "pathItems" {
			if err := n.validator.validatePathItem(mapping, n.version); err != nil {
				return n.sources.fail(file, pointer, "api.structure", "invalid referenced path item")
			}
		} else if kind == "media" && len(n.version) >= 4 && n.version[:4] == "3.2." {
			if err := n.validator.validateObject("mediaTypes", mapping, n.version); err != nil {
				return n.sources.fail(file, pointer, "api.structure", "invalid referenced media type")
			}
		} else if kind != "operation" && kind != "media" && kind != "encoding" {
			if err := n.validator.validateObject(kind, mapping, n.version); err != nil {
				return n.sources.fail(file, pointer, "api.structure", "invalid referenced "+kind+" object")
			}
		}
	}
	scan := func(name, childKind string) error {
		child, present := mapping[name]
		if !present {
			return nil
		}
		return n.scanTyped(file, pointer+"/"+escapePointer(name), child, childKind, seen, depth+1)
	}
	scanMap := func(name, childKind string) error {
		for _, childName := range sortedKeys(object(mapping[name])) {
			if (name == "paths" || name == "responses") && len(childName) >= 2 && childName[:2] == "x-" {
				continue
			}
			if err := n.scanTyped(file, pointer+"/"+escapePointer(name)+"/"+escapePointer(childName), object(mapping[name])[childName], childKind, seen, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	scanList := func(name, childKind string) error {
		for index, child := range array(mapping[name]) {
			if err := n.scanTyped(file, pointer+"/"+escapePointer(name)+"/"+strconv.Itoa(index), child, childKind, seen, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	switch kind {
	case "document":
		if err := scanMap("paths", "pathItems"); err != nil {
			return err
		}
		if err := scanMap("webhooks", "pathItems"); err != nil {
			return err
		}
		components := object(mapping["components"])
		for _, componentKind := range []string{"schemas", "parameters", "headers", "requestBodies", "responses", "securitySchemes", "examples", "links", "callbacks", "pathItems", "mediaTypes"} {
			for _, name := range sortedKeys(object(components[componentKind])) {
				childKind := componentKind
				switch childKind {
				case "schemas":
					childKind = "schema"
				case "mediaTypes":
					childKind = "media"
				}
				if err := n.scanTyped(file, pointer+"/components/"+componentKind+"/"+escapePointer(name), object(components[componentKind])[name], childKind, seen, depth+1); err != nil {
					return err
				}
			}
		}
	case "pathItems":
		if err := scanList("parameters", "parameters"); err != nil {
			return err
		}
		for _, method := range []string{"get", "put", "post", "delete", "options", "head", "patch", "trace", "query"} {
			if err := scan(method, "operation"); err != nil {
				return err
			}
		}
		return scanMap("additionalOperations", "operation")
	case "operation":
		if err := scanList("parameters", "parameters"); err != nil {
			return err
		}
		if err := scan("requestBody", "requestBodies"); err != nil {
			return err
		}
		if err := scanMap("responses", "responses"); err != nil {
			return err
		}
		return scanMap("callbacks", "callbacks")
	case "parameters", "headers":
		if err := scan("schema", "schema"); err != nil {
			return err
		}
		if err := scanMap("examples", "examples"); err != nil {
			return err
		}
		return scanMap("content", "media")
	case "requestBodies":
		return scanMap("content", "media")
	case "responses":
		if err := scanMap("headers", "headers"); err != nil {
			return err
		}
		if err := scanMap("links", "links"); err != nil {
			return err
		}
		return scanMap("content", "media")
	case "media":
		if err := scan("schema", "schema"); err != nil {
			return err
		}
		if err := scan("itemSchema", "schema"); err != nil {
			return err
		}
		if err := scanMap("examples", "examples"); err != nil {
			return err
		}
		if err := scanMap("encoding", "encoding"); err != nil {
			return err
		}
		if err := scanList("prefixEncoding", "encoding"); err != nil {
			return err
		}
		return scan("itemEncoding", "encoding")
	case "encoding":
		if err := scanMap("headers", "headers"); err != nil {
			return err
		}
		if err := scanMap("encoding", "encoding"); err != nil {
			return err
		}
		if err := scanList("prefixEncoding", "encoding"); err != nil {
			return err
		}
		return scan("itemEncoding", "encoding")
	case "callbacks":
		for _, expression := range sortedKeys(mapping) {
			if len(expression) >= 2 && expression[:2] == "x-" {
				continue
			}
			if err := n.scanTyped(file, pointer+"/"+escapePointer(expression), mapping[expression], "pathItems", seen, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}
