package importer

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/kordax/pb-md5-generator/document"
	protosource "github.com/kordax/pb-md5-generator/internal/proto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

type protoType struct {
	id      string
	name    string
	file    *descriptorpb.FileDescriptorProto
	path    []int32
	message *descriptorpb.DescriptorProto
	enum    *descriptorpb.EnumDescriptorProto
	ignored bool
}

type protobufImporter struct {
	ctx       context.Context
	api       *document.API
	types     map[string]*protoType
	opaque    map[string]bool
	locations map[string]map[string]*descriptorpb.SourceCodeInfo_Location
}

// Protobuf parses a bounded source snapshot without resolving imports or reading
// files. Use ProtobufRequest when descriptors for imported types are available.
func Protobuf(ctx context.Context, name string, data []byte) (*document.API, error) {
	if err := CheckProtobufSource(ctx, name, data); err != nil {
		return nil, err
	}
	request, err := protosource.RequestFromSource(name, data)
	if err != nil {
		return nil, &Error{Code: "invalid-protobuf", Source: document.Location{File: name}, Message: "invalid protobuf source", Cause: err}
	}
	return ProtobufRequest(ctx, name, request)
}

// ProtobufRequest reuses parsed descriptors and keeps gRPC methods separate from
// HTTP operations. It never reads dependency files or invents HTTP bindings.
func ProtobufRequest(ctx context.Context, name string, request *pluginpb.CodeGeneratorRequest) (*document.API, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validSourceName(name) {
		return nil, &Error{Code: "invalid-source", Source: document.Location{File: name}, Message: "source must be a local project-relative path"}
	}
	if request == nil || len(request.ProtoFile) == 0 {
		return nil, &Error{Code: "invalid-protobuf", Source: document.Location{File: name}, Message: "no protobuf file descriptors supplied"}
	}
	if len(request.ProtoFile) > MaxDocuments {
		return nil, &Error{Code: "source-limit", Source: document.Location{File: name}, Message: "protobuf descriptor file limit exceeded"}
	}
	if err := boundDescriptor(ctx, reflect.ValueOf(request), map[uintptr]bool{}, 0, new(int), new(int)); err != nil {
		return nil, &Error{Code: "source-limit", Source: document.Location{File: name}, Message: "protobuf descriptor graph exceeds limits", Cause: err}
	}
	visible, opaque, _, err := visibleProtobufRequest(ctx, request)
	if err != nil {
		return nil, err
	}
	request = visible
	p := &protobufImporter{ctx: ctx, types: map[string]*protoType{}, opaque: opaque, locations: map[string]map[string]*descriptorpb.SourceCodeInfo_Location{}}
	p.api = &document.API{ID: name, Protocol: "grpc", Format: "protobuf", Version: "proto3", Title: name, Source: document.Location{File: name}, Servers: []document.Server{}, Tags: []document.Tag{}, Operations: []document.Operation{}, Schemas: []document.Schema{}, SecuritySchemes: []document.SecurityScheme{}, Security: []document.SecurityRequirement{}, Documents: []document.APIDocument{}}
	var root *descriptorpb.FileDescriptorProto
	files := append([]*descriptorpb.FileDescriptorProto(nil), request.ProtoFile...)
	sort.Slice(files, func(i, j int) bool { return files[i].GetName() < files[j].GetName() })
	seen := map[string]bool{}
	for _, file := range files {
		if file == nil || !validSourceName(file.GetName()) || seen[file.GetName()] {
			return nil, p.fail(file, nil, "invalid-protobuf", "descriptor file names must be unique local paths")
		}
		seen[file.GetName()] = true
		if file.GetName() == name {
			root = file
		}
		p.locations[file.GetName()] = map[string]*descriptorpb.SourceCodeInfo_Location{}
		for _, location := range file.GetSourceCodeInfo().GetLocation() {
			p.locations[file.GetName()][protoPath(location.GetPath())] = location
		}
		encoded, err := canonicalProtoJSON(file, true)
		if err != nil {
			return nil, p.fail(file, nil, "invalid-protobuf", "cannot serialize protobuf descriptor")
		}
		if len(encoded) > MaxDocumentBytes {
			return nil, p.fail(file, nil, "source-limit", "protobuf descriptor exceeds the document byte limit")
		}
		p.api.Documents = append(p.api.Documents, document.APIDocument{Path: file.GetName(), Content: encoded})
		if err := p.register(file, file.GetPackage(), nil, file.GetMessageType(), file.GetEnumType(), 0); err != nil {
			return nil, err
		}
	}
	if root == nil {
		return nil, &Error{Code: "invalid-protobuf", Source: document.Location{File: name}, Message: "requested protobuf descriptor is missing"}
	}
	if root.GetSyntax() != "" {
		p.api.Version = root.GetSyntax()
	} else {
		p.api.Version = "proto2"
	}
	if root.GetPackage() != "" {
		p.api.Title = root.GetPackage()
	}
	p.api.Description = p.description(root, nil)
	typeNames := make([]string, 0, len(p.types))
	for name := range p.types {
		typeNames = append(typeNames, name)
	}
	sort.Strings(typeNames)
	for _, key := range typeNames {
		typeInfo := p.types[key]
		if typeInfo.message != nil && typeInfo.message.GetOptions().GetMapEntry() {
			continue
		}
		schema, err := p.schema(typeInfo)
		if err != nil {
			return nil, err
		}
		p.api.Schemas = append(p.api.Schemas, schema)
	}
	for serviceIndex, service := range root.GetService() {
		servicePath := []int32{6, int32(serviceIndex)}
		if service == nil || service.GetName() == "" {
			return nil, p.fail(root, servicePath, "invalid-protobuf", "service must have a name")
		}
		serviceName := qualified(root.GetPackage(), service.GetName())
		p.api.Tags = append(p.api.Tags, document.Tag{Name: serviceName, Description: p.description(root, servicePath)})
		for methodIndex, method := range service.GetMethod() {
			methodPath := appendPath(servicePath, 2, int32(methodIndex))
			if len(p.api.Operations) >= MaxOperations {
				return nil, p.fail(root, methodPath, "operation-limit", "protobuf method limit exceeded")
			}
			if method == nil || method.GetName() == "" {
				return nil, p.fail(root, methodPath, "invalid-protobuf", "method must have a name")
			}
			input, err := p.lookup(root, methodPath, method.GetInputType())
			if err != nil {
				return nil, err
			}
			output, err := p.lookup(root, methodPath, method.GetOutputType())
			if err != nil {
				return nil, err
			}
			if (!input.ignored && input.message == nil) || (!output.ignored && output.message == nil) || input.message.GetOptions().GetMapEntry() || output.message.GetOptions().GetMapEntry() {
				return nil, p.fail(root, methodPath, "invalid-protobuf", "RPC input and output must be message types")
			}
			id := root.GetName() + "#" + protoPointer(methodPath)
			encoded, err := canonicalProtoJSON(method, false)
			if err != nil {
				return nil, p.fail(root, methodPath, "invalid-protobuf", "cannot serialize RPC descriptor")
			}
			inputID, outputID := input.id, output.id
			if input.ignored {
				inputID = input.name
			}
			if output.ignored {
				outputID = output.name
			}
			p.api.Operations = append(p.api.Operations, document.Operation{ID: id, Anchor: document.APIAnchor("operation", id), Interaction: "rpc", Path: "/" + serviceName + "/" + method.GetName(), Service: serviceName, RPCMethod: method.GetName(), Description: p.description(root, methodPath), Tags: []string{serviceName}, Deprecated: method.GetOptions().GetDeprecated(), Servers: []document.Server{}, Security: []document.SecurityRequirement{}, InputSchema: inputID, OutputSchema: outputID, ClientStreaming: method.GetClientStreaming(), ServerStreaming: method.GetServerStreaming(), Source: p.location(root, methodPath), Raw: encoded})
		}
	}
	sort.Slice(p.api.Schemas, func(i, j int) bool { return p.api.Schemas[i].ID < p.api.Schemas[j].ID })
	if err := checkModelSize(p.api); err != nil {
		return nil, p.fail(root, nil, "api.limit", "normalized protobuf model exceeds its byte or node budget")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return p.api, nil
}

func boundDescriptor(ctx context.Context, value reflect.Value, active map[uintptr]bool, depth int, nodes, bytes *int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	*nodes++
	if depth > MaxDepth || *nodes > MaxNodes || *bytes > MaxSourceBytes {
		return fmt.Errorf("descriptor limit exceeded")
	}
	if !value.IsValid() {
		return nil
	}
	switch value.Kind() {
	case reflect.Interface:
		if !value.IsNil() {
			return boundDescriptor(ctx, value.Elem(), active, depth+1, nodes, bytes)
		}
	case reflect.Pointer:
		if value.IsNil() {
			return nil
		}
		pointer := value.Pointer()
		if active[pointer] {
			return fmt.Errorf("cyclic descriptor objects")
		}
		active[pointer] = true
		var err error
		if value.CanInterface() {
			if message, ok := value.Interface().(proto.Message); ok {
				err = boundProtoMessage(ctx, message.ProtoReflect(), active, depth+1, nodes, bytes)
			} else {
				err = boundDescriptor(ctx, value.Elem(), active, depth+1, nodes, bytes)
			}
		} else {
			err = boundDescriptor(ctx, value.Elem(), active, depth+1, nodes, bytes)
		}
		delete(active, pointer)
		return err
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			if value.Type().Field(index).PkgPath != "" {
				continue
			}
			if err := boundDescriptor(ctx, value.Field(index), active, depth+1, nodes, bytes); err != nil {
				return err
			}
		}
	case reflect.Map:
		if value.Len() > MaxNodes {
			return fmt.Errorf("descriptor collection limit exceeded")
		}
		iterator := value.MapRange()
		for iterator.Next() {
			if err := boundDescriptor(ctx, iterator.Value(), active, depth+1, nodes, bytes); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			*bytes += value.Len()
			break
		}
		if value.Len() > MaxNodes {
			return fmt.Errorf("descriptor collection limit exceeded")
		}
		for index := 0; index < value.Len(); index++ {
			if err := boundDescriptor(ctx, value.Index(index), active, depth+1, nodes, bytes); err != nil {
				return err
			}
		}
	case reflect.String:
		*bytes += value.Len()
	}
	if *bytes > MaxSourceBytes {
		return fmt.Errorf("descriptor byte limit exceeded")
	}
	return nil
}

func boundProtoMessage(ctx context.Context, message protoreflect.Message, active map[uintptr]bool, depth int, nodes, bytes *int) error {
	*bytes += len(message.GetUnknown())
	if *bytes > MaxSourceBytes {
		return fmt.Errorf("descriptor byte limit exceeded")
	}
	var failure error
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.IsMap() {
			if value.Map().Len() > MaxNodes {
				failure = fmt.Errorf("descriptor collection limit exceeded")
				return false
			}
			value.Map().Range(func(key protoreflect.MapKey, entry protoreflect.Value) bool {
				failure = boundProtoScalar(ctx, field.MapKey().Kind(), key.Value(), active, depth+1, nodes, bytes)
				if failure == nil {
					failure = boundProtoScalar(ctx, field.MapValue().Kind(), entry, active, depth+1, nodes, bytes)
				}
				return failure == nil
			})
		} else if field.IsList() {
			if value.List().Len() > MaxNodes {
				failure = fmt.Errorf("descriptor collection limit exceeded")
				return false
			}
			for index := 0; index < value.List().Len(); index++ {
				failure = boundProtoScalar(ctx, field.Kind(), value.List().Get(index), active, depth+1, nodes, bytes)
				if failure != nil {
					break
				}
			}
		} else {
			failure = boundProtoScalar(ctx, field.Kind(), value, active, depth+1, nodes, bytes)
		}
		return failure == nil
	})
	return failure
}

func boundProtoScalar(ctx context.Context, kind protoreflect.Kind, value protoreflect.Value, active map[uintptr]bool, depth int, nodes, bytes *int) error {
	if kind == protoreflect.MessageKind || kind == protoreflect.GroupKind {
		return boundDescriptor(ctx, reflect.ValueOf(value.Message().Interface()), active, depth, nodes, bytes)
	}
	return boundDescriptor(ctx, reflect.ValueOf(value.Interface()), active, depth, nodes, bytes)
}

func (p *protobufImporter) register(file *descriptorpb.FileDescriptorProto, prefix string, path []int32, messages []*descriptorpb.DescriptorProto, enums []*descriptorpb.EnumDescriptorProto, depth int) error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	if depth > MaxDepth {
		return p.fail(file, path, "schema-limit", "protobuf nesting depth limit exceeded")
	}
	messageField, enumField := int32(4), int32(5)
	if len(path) > 0 {
		messageField, enumField = 3, 4
	}
	for index, message := range messages {
		current := appendPath(path, messageField, int32(index))
		if message == nil || message.GetName() == "" {
			return p.fail(file, current, "invalid-protobuf", "message must have a name")
		}
		name := qualified(prefix, message.GetName())
		if err := p.addType(&protoType{id: file.GetName() + "#" + protoPointer(current), name: name, file: file, path: current, message: message}); err != nil {
			return err
		}
		if err := p.register(file, name, current, message.GetNestedType(), message.GetEnumType(), depth+1); err != nil {
			return err
		}
	}
	for index, enum := range enums {
		current := appendPath(path, enumField, int32(index))
		if enum == nil || enum.GetName() == "" {
			return p.fail(file, current, "invalid-protobuf", "enum must have a name")
		}
		name := qualified(prefix, enum.GetName())
		if err := p.addType(&protoType{id: file.GetName() + "#" + protoPointer(current), name: name, file: file, path: current, enum: enum}); err != nil {
			return err
		}
	}
	return nil
}

func (p *protobufImporter) addType(value *protoType) error {
	if p.types[value.name] != nil {
		return p.fail(value.file, value.path, "invalid-protobuf", "protobuf type names must be unique")
	}
	if len(p.types) >= MaxSchemas {
		return p.fail(value.file, value.path, "schema-limit", "protobuf type limit exceeded")
	}
	p.types[value.name] = value
	return nil
}

func (p *protobufImporter) lookup(file *descriptorpb.FileDescriptorProto, path []int32, name string) (*protoType, error) {
	if strings.HasPrefix(name, ".") {
		candidate := strings.TrimPrefix(name, ".")
		if value := p.types[candidate]; value != nil {
			return value, nil
		}
		if p.opaque[candidate] {
			return &protoType{name: candidate, ignored: true}, nil
		}
	} else {
		scope := file.GetPackage()
		messages := file.GetMessageType()
		for index := 0; index+1 < len(path); index += 2 {
			if (index == 0 && path[index] != 4) || (index > 0 && path[index] != 3) {
				break
			}
			if path[index+1] < 0 || int(path[index+1]) >= len(messages) {
				break
			}
			message := messages[path[index+1]]
			scope = qualified(scope, message.GetName())
			messages = message.GetNestedType()
		}
		for {
			candidate := qualified(scope, name)
			if value := p.types[candidate]; value != nil {
				return value, nil
			}
			if p.opaque[candidate] {
				return &protoType{name: candidate, ignored: true}, nil
			}
			if scope == "" {
				break
			}
			if separator := strings.LastIndexByte(scope, '.'); separator >= 0 {
				scope = scope[:separator]
			} else {
				scope = ""
			}
		}
	}
	return nil, p.fail(file, path, "unresolved-protobuf-type", "referenced protobuf type has no supplied descriptor: "+name)
}

func (p *protobufImporter) schema(value *protoType) (document.Schema, error) {
	definition := map[string]any{"title": value.name, "x-protobuf-full-name": value.name}
	if format := wellKnownJSONForm(value.name); format != "" {
		definition["x-protobuf-json-format"] = format
	}
	if description := p.description(value.file, value.path); description != "" {
		definition["description"] = description
	}
	references := []document.SchemaReference{}
	if value.enum != nil {
		definition["type"] = "string"
		names := []string{}
		values := []map[string]any{}
		for _, entry := range value.enum.GetValue() {
			names = append(names, entry.GetName())
			values = append(values, map[string]any{"name": entry.GetName(), "number": entry.GetNumber()})
		}
		definition["enum"] = names
		definition["x-protobuf-enum-values"] = values
	} else {
		definition["type"] = "object"
		properties := map[string]any{}
		required := []string{}
		numbers := map[int32]bool{}
		for index, field := range value.message.GetField() {
			fieldPath := appendPath(value.path, 2, int32(index))
			if field == nil || field.GetName() == "" || field.GetNumber() <= 0 || numbers[field.GetNumber()] {
				return document.Schema{}, p.fail(value.file, fieldPath, "invalid-protobuf", "fields must have unique positive numbers and nonempty names")
			}
			numbers[field.GetNumber()] = true
			name := field.GetJsonName()
			if name == "" {
				name = protoJSONName(field.GetName())
			}
			if _, exists := properties[name]; exists {
				return document.Schema{}, p.fail(value.file, fieldPath, "invalid-protobuf", "field JSON names must be unique")
			}
			fieldSchema, err := p.fieldSchema(value.file, fieldPath, field)
			if err != nil {
				return document.Schema{}, err
			}
			if description := p.description(value.file, fieldPath); description != "" {
				fieldSchema["description"] = description
			}
			if field.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REQUIRED {
				required = append(required, name)
			}
			properties[name] = fieldSchema
			collectProtoReferences(fieldSchema, "/properties/"+escapePointer(name), &references)
		}
		definition["properties"] = properties
		if len(required) > 0 {
			definition["required"] = required
		}
		if len(value.message.GetOneofDecl()) > 0 {
			oneofs := []map[string]any{}
			for index, oneof := range value.message.GetOneofDecl() {
				fields := []string{}
				for _, field := range value.message.GetField() {
					if field.OneofIndex != nil && int(field.GetOneofIndex()) == index {
						fields = append(fields, field.GetName())
					}
				}
				oneofs = append(oneofs, map[string]any{"name": oneof.GetName(), "fields": fields})
			}
			definition["x-protobuf-oneofs"] = oneofs
		}
	}
	return document.Schema{ID: value.id, Anchor: document.APIAnchor("schema", value.id), Name: value.name, Definition: raw(definition), References: references, Source: p.location(value.file, value.path)}, nil
}

func (p *protobufImporter) fieldSchema(file *descriptorpb.FileDescriptorProto, path []int32, field *descriptorpb.FieldDescriptorProto) (map[string]any, error) {
	definition := map[string]any{"x-protobuf-name": field.GetName(), "x-protobuf-number": field.GetNumber(), "x-protobuf-type": strings.TrimPrefix(field.GetType().String(), "TYPE_")}
	switch field.GetType() {
	case descriptorpb.FieldDescriptorProto_TYPE_DOUBLE, descriptorpb.FieldDescriptorProto_TYPE_FLOAT:
		definition["type"] = "number"
		definition["format"] = strings.ToLower(strings.TrimPrefix(field.GetType().String(), "TYPE_"))
	case descriptorpb.FieldDescriptorProto_TYPE_INT64, descriptorpb.FieldDescriptorProto_TYPE_SINT64, descriptorpb.FieldDescriptorProto_TYPE_SFIXED64:
		definition["type"] = "integer"
		definition["format"] = "int64"
		definition["x-protobuf-json-type"] = "string"
	case descriptorpb.FieldDescriptorProto_TYPE_UINT64, descriptorpb.FieldDescriptorProto_TYPE_FIXED64:
		definition["type"] = "integer"
		definition["format"] = "uint64"
		definition["minimum"] = 0
		definition["x-protobuf-json-type"] = "string"
	case descriptorpb.FieldDescriptorProto_TYPE_INT32, descriptorpb.FieldDescriptorProto_TYPE_SINT32, descriptorpb.FieldDescriptorProto_TYPE_SFIXED32:
		definition["type"] = "integer"
		definition["format"] = "int32"
	case descriptorpb.FieldDescriptorProto_TYPE_UINT32, descriptorpb.FieldDescriptorProto_TYPE_FIXED32:
		definition["type"] = "integer"
		definition["format"] = "uint32"
		definition["minimum"] = 0
	case descriptorpb.FieldDescriptorProto_TYPE_BOOL:
		definition["type"] = "boolean"
	case descriptorpb.FieldDescriptorProto_TYPE_STRING:
		definition["type"] = "string"
	case descriptorpb.FieldDescriptorProto_TYPE_BYTES:
		definition["type"] = "string"
		definition["contentEncoding"] = "base64"
	case descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, descriptorpb.FieldDescriptorProto_TYPE_GROUP, descriptorpb.FieldDescriptorProto_TYPE_ENUM:
		target, err := p.lookup(file, path, field.GetTypeName())
		if err != nil {
			return nil, err
		}
		if target.ignored {
			definition["x-protobuf-full-name"] = target.name
			definition["x-protobuf-opaque"] = true
		} else if target.message != nil && target.message.GetOptions().GetMapEntry() {
			if field.GetLabel() != descriptorpb.FieldDescriptorProto_LABEL_REPEATED || len(target.message.GetField()) != 2 {
				return nil, p.fail(file, path, "invalid-protobuf", "map entry must have a repeated field and key/value descriptors")
			}
			valueField := target.message.GetField()[1]
			if valueType := p.types[strings.TrimPrefix(valueField.GetTypeName(), ".")]; valueType != nil && valueType.message.GetOptions().GetMapEntry() {
				return nil, p.fail(file, path, "invalid-protobuf", "nested map entries are not valid protobuf map values")
			}
			value, err := p.fieldSchema(target.file, appendPath(target.path, 2, 1), valueField)
			if err != nil {
				return nil, err
			}
			definition["type"] = "object"
			definition["additionalProperties"] = value
			definition["x-protobuf-map-key-type"] = strings.TrimPrefix(target.message.GetField()[0].GetType().String(), "TYPE_")
			return definition, nil
		}
		if !target.ignored {
			definition["$ref"] = target.id
		}
	default:
		return nil, p.fail(file, path, "invalid-protobuf", "unknown protobuf field type")
	}
	if field.DefaultValue != nil {
		definition["x-protobuf-default"] = field.GetDefaultValue()
	}
	if field.GetOptions().GetDeprecated() {
		definition["deprecated"] = true
	}
	if field.GetProto3Optional() {
		definition["x-protobuf-optional"] = true
	}
	if field.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED {
		return map[string]any{"type": "array", "items": definition, "x-protobuf-name": field.GetName(), "x-protobuf-number": field.GetNumber()}, nil
	}
	return definition, nil
}

func wellKnownJSONForm(name string) string {
	switch name {
	case "google.protobuf.Timestamp":
		return "RFC3339 string"
	case "google.protobuf.Duration":
		return "duration string"
	case "google.protobuf.FieldMask":
		return "comma-separated field paths"
	case "google.protobuf.Any":
		return "object with @type and embedded value"
	case "google.protobuf.Struct":
		return "JSON object"
	case "google.protobuf.ListValue":
		return "JSON array"
	case "google.protobuf.Value":
		return "any JSON value"
	case "google.protobuf.DoubleValue", "google.protobuf.FloatValue":
		return "JSON number"
	case "google.protobuf.Int64Value", "google.protobuf.UInt64Value":
		return "decimal integer string"
	case "google.protobuf.Int32Value", "google.protobuf.UInt32Value":
		return "JSON integer"
	case "google.protobuf.BoolValue":
		return "JSON boolean"
	case "google.protobuf.StringValue":
		return "JSON string"
	case "google.protobuf.BytesValue":
		return "base64 string"
	default:
		return ""
	}
}

func collectProtoReferences(value map[string]any, pointer string, result *[]document.SchemaReference) {
	if ref := text(value["$ref"]); ref != "" {
		*result = append(*result, document.SchemaReference{Pointer: pointer + "/$ref", Reference: ref, Target: ref})
	}
	for _, key := range []string{"items", "additionalProperties"} {
		if child := object(value[key]); child != nil {
			collectProtoReferences(child, pointer+"/"+key, result)
		}
	}
}

func (p *protobufImporter) location(file *descriptorpb.FileDescriptorProto, path []int32) document.Location {
	location := document.Location{}
	if file == nil {
		return location
	}
	location.File = file.GetName()
	if position := p.locations[file.GetName()][protoPath(path)]; position != nil && len(position.GetSpan()) >= 2 {
		location.Line = int(position.GetSpan()[0]) + 1
		location.Column = int(position.GetSpan()[1]) + 1
	}
	return location
}

func (p *protobufImporter) description(file *descriptorpb.FileDescriptorProto, path []int32) string {
	if position := p.locations[file.GetName()][protoPath(path)]; position != nil {
		return strings.TrimSpace(strings.Join(append(append([]string{}, position.GetLeadingDetachedComments()...), position.GetLeadingComments(), position.GetTrailingComments()), "\n\n"))
	}
	return ""
}

func (p *protobufImporter) fail(file *descriptorpb.FileDescriptorProto, path []int32, code, message string) error {
	return &Error{Code: code, Source: p.location(file, path), Pointer: protoPointer(path), Message: message}
}

func appendPath(path []int32, tail ...int32) []int32 {
	return append(append([]int32(nil), path...), tail...)
}
func qualified(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}
func protoPath(path []int32) string {
	parts := make([]string, len(path))
	for index, part := range path {
		parts[index] = strconv.Itoa(int(part))
	}
	return strings.Join(parts, "/")
}
func protoPointer(path []int32) string {
	var pointer strings.Builder
	kind := "file"
	for index := 0; index+1 < len(path); index += 2 {
		field := strconv.Itoa(int(path[index]))
		switch kind {
		case "file":
			switch path[index] {
			case 4:
				field, kind = "messageType", "message"
			case 5:
				field, kind = "enumType", "enum"
			case 6:
				field, kind = "service", "service"
			}
		case "message":
			switch path[index] {
			case 2:
				field, kind = "field", "field"
			case 3:
				field = "nestedType"
			case 4:
				field, kind = "enumType", "enum"
			case 8:
				field, kind = "oneofDecl", "oneof"
			}
		case "service":
			if path[index] == 2 {
				field, kind = "method", "method"
			}
		case "enum":
			if path[index] == 2 {
				field, kind = "value", "value"
			}
		}
		pointer.WriteString("/" + field + "/" + strconv.Itoa(int(path[index+1])))
	}
	return pointer.String()
}
func protoJSONName(name string) string {
	var out strings.Builder
	upper := false
	for _, part := range name {
		if part == '_' {
			upper = true
			continue
		}
		if upper && part >= 'a' && part <= 'z' {
			part -= 32
		}
		out.WriteRune(part)
		upper = false
	}
	return out.String()
}
