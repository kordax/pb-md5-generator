package importer

import (
	"context"
	"strings"

	"github.com/kordax/pb-md5-generator/internal/parser"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

type protoVisibility struct {
	locations map[string]*descriptorpb.SourceCodeInfo_Location
	remapped  map[string][]int32
	dropped   map[string]bool
	opaque    map[string]bool
	usedMaps  map[string]bool
	filtered  bool
}

// Filter before producing schemas or retained descriptors. Keeping the source
// snapshot or original descriptor wire after filtering would disclose @ignore.
func visibleProtobufRequest(ctx context.Context, request *pluginpb.CodeGeneratorRequest) (*pluginpb.CodeGeneratorRequest, map[string]bool, bool, error) {
	visible := proto.Clone(request).(*pluginpb.CodeGeneratorRequest)
	opaque := map[string]bool{}
	filtered := false
	for _, file := range visible.GetProtoFile() {
		if err := ctx.Err(); err != nil {
			return nil, nil, false, err
		}
		if file == nil {
			continue
		}
		visibility := &protoVisibility{locations: map[string]*descriptorpb.SourceCodeInfo_Location{}, remapped: map[string][]int32{}, dropped: map[string]bool{}, opaque: opaque, usedMaps: map[string]bool{}}
		for _, location := range file.GetSourceCodeInfo().GetLocation() {
			visibility.locations[protoPath(location.GetPath())] = location
		}
		if visibility.ignored(nil) {
			for _, message := range file.GetMessageType() {
				visibility.hideMessage(file.GetPackage(), message)
			}
			for _, enum := range file.GetEnumType() {
				opaque[qualified(file.GetPackage(), enum.GetName())] = true
			}
			file.MessageType, file.EnumType, file.Service, file.Extension = nil, nil, nil, nil
			file.Options, file.SourceCodeInfo = nil, nil
			file.ProtoReflect().SetUnknown(nil)
			filtered = true
			continue
		}
		file.MessageType = visibility.messages(file.GetPackage(), nil, nil, 4, file.GetMessageType())
		file.EnumType = visibility.enums(file.GetPackage(), nil, nil, 5, file.GetEnumType())
		file.Extension = visibility.fields(nil, nil, 7, file.GetExtension(), nil)
		services := file.GetService()[:0]
		for index, service := range file.GetService() {
			oldPath := []int32{6, int32(index)}
			if visibility.omit(oldPath) {
				continue
			}
			newPath := []int32{6, boundedProtoIndex(len(services))}
			visibility.keep(oldPath, newPath)
			if service != nil {
				methods := service.GetMethod()[:0]
				for methodIndex, method := range service.GetMethod() {
					oldMethod := appendPath(oldPath, 2, int32(methodIndex))
					if visibility.omit(oldMethod) {
						continue
					}
					visibility.keep(oldMethod, appendPath(newPath, 2, boundedProtoIndex(len(methods))))
					methods = append(methods, method)
				}
				service.Method = methods
			}
			services = append(services, service)
		}
		file.Service = services
		if visibility.filtered {
			visibility.sourceInfo(file)
			filtered = true
		}
	}
	return visible, opaque, filtered, nil
}

func (v *protoVisibility) ignored(path []int32) bool {
	location := v.locations[protoPath(path)]
	if location == nil {
		return false
	}
	parts := append([]string{}, location.GetLeadingDetachedComments()...)
	parts = append(parts, location.GetLeadingComments(), location.GetTrailingComments())
	for _, comment := range parts {
		if parser.CommentIgnored(comment) {
			return true
		}
	}
	return false
}

func (v *protoVisibility) omit(path []int32) bool {
	if !v.ignored(path) {
		return false
	}
	v.filtered = true
	v.dropped[protoPath(path)] = true
	return true
}

// Every caller first passes boundDescriptor. Treat an index outside that
// validated graph as an internal invariant violation before narrowing it.
func boundedProtoIndex(index int) int32 {
	if index < 0 || index > MaxNodes {
		panic("descriptor index exceeds the validated graph budget")
	}
	return int32(index)
}

func (v *protoVisibility) keep(oldPath, newPath []int32) {
	v.remapped[protoPath(oldPath)] = appendPath(newPath)
}

func (v *protoVisibility) hideMessage(prefix string, message *descriptorpb.DescriptorProto) {
	if message == nil {
		return
	}
	name := qualified(prefix, message.GetName())
	v.opaque[name] = true
	for _, child := range message.GetNestedType() {
		v.hideMessage(name, child)
	}
	for _, enum := range message.GetEnumType() {
		v.opaque[qualified(name, enum.GetName())] = true
	}
}

func (v *protoVisibility) messages(prefix string, oldParent, newParent []int32, field int32, values []*descriptorpb.DescriptorProto) []*descriptorpb.DescriptorProto {
	result := values[:0]
	for index, message := range values {
		oldPath := appendPath(oldParent, field, int32(index))
		orphanMap := len(oldParent) > 0 && message.GetOptions().GetMapEntry() && !v.usedMaps[qualified(prefix, message.GetName())]
		if v.omit(oldPath) || orphanMap {
			v.filtered = true
			v.dropped[protoPath(oldPath)] = true
			v.hideMessage(prefix, message)
			continue
		}
		newPath := appendPath(newParent, field, boundedProtoIndex(len(result)))
		v.keep(oldPath, newPath)
		if message != nil {
			name := qualified(prefix, message.GetName())
			oneofIndices := map[int32]int32{}
			oneofs := message.GetOneofDecl()[:0]
			for oneofIndex, oneof := range message.GetOneofDecl() {
				oldOneof := appendPath(oldPath, 8, int32(oneofIndex))
				if v.omit(oldOneof) {
					oneofIndices[int32(oneofIndex)] = -1
					continue
				}
				oneofIndices[int32(oneofIndex)] = boundedProtoIndex(len(oneofs))
				v.keep(oldOneof, appendPath(newPath, 8, boundedProtoIndex(len(oneofs))))
				oneofs = append(oneofs, oneof)
			}
			message.OneofDecl = oneofs
			message.Field = v.fields(oldPath, newPath, 2, message.GetField(), oneofIndices)
			message.Extension = v.fields(oldPath, newPath, 6, message.GetExtension(), nil)
			for _, field := range message.GetField() {
				typeName := field.GetTypeName()
				if strings.HasPrefix(typeName, ".") {
					v.usedMaps[strings.TrimPrefix(typeName, ".")] = true
				} else if typeName != "" {
					scope := name
					for {
						v.usedMaps[qualified(scope, typeName)] = true
						if scope == "" {
							break
						}
						if dot := strings.LastIndexByte(scope, '.'); dot >= 0 {
							scope = scope[:dot]
						} else {
							scope = ""
						}
					}
				}
			}
			message.NestedType = v.messages(name, oldPath, newPath, 3, message.GetNestedType())
			message.EnumType = v.enums(name, oldPath, newPath, 4, message.GetEnumType())
		}
		result = append(result, message)
	}
	return result
}

func (v *protoVisibility) enums(prefix string, oldParent, newParent []int32, field int32, values []*descriptorpb.EnumDescriptorProto) []*descriptorpb.EnumDescriptorProto {
	result := values[:0]
	for index, enum := range values {
		oldPath := appendPath(oldParent, field, int32(index))
		if v.omit(oldPath) {
			v.opaque[qualified(prefix, enum.GetName())] = true
			continue
		}
		newPath := appendPath(newParent, field, boundedProtoIndex(len(result)))
		v.keep(oldPath, newPath)
		if enum != nil {
			entries := enum.GetValue()[:0]
			for entryIndex, entry := range enum.GetValue() {
				oldEntry := appendPath(oldPath, 2, int32(entryIndex))
				if v.omit(oldEntry) {
					continue
				}
				v.keep(oldEntry, appendPath(newPath, 2, boundedProtoIndex(len(entries))))
				entries = append(entries, entry)
			}
			enum.Value = entries
		}
		result = append(result, enum)
	}
	return result
}

func (v *protoVisibility) fields(oldParent, newParent []int32, field int32, values []*descriptorpb.FieldDescriptorProto, oneofs map[int32]int32) []*descriptorpb.FieldDescriptorProto {
	result := values[:0]
	for index, value := range values {
		oldPath := appendPath(oldParent, field, int32(index))
		hiddenOneof := false
		if value != nil && value.OneofIndex != nil && oneofs != nil {
			if mapped, exists := oneofs[value.GetOneofIndex()]; exists {
				hiddenOneof = mapped < 0
				if !hiddenOneof {
					value.OneofIndex = proto.Int32(mapped)
				}
			}
		}
		if v.omit(oldPath) {
			continue
		}
		if hiddenOneof {
			v.filtered = true
			v.dropped[protoPath(oldPath)] = true
			continue
		}
		v.keep(oldPath, appendPath(newParent, field, boundedProtoIndex(len(result))))
		result = append(result, value)
	}
	return result
}

func (v *protoVisibility) sourceInfo(file *descriptorpb.FileDescriptorProto) {
	info := file.GetSourceCodeInfo()
	if info == nil {
		return
	}
	locations := info.GetLocation()[:0]
	for _, location := range info.GetLocation() {
		oldPath := location.GetPath()
		dropped := false
		for length := len(oldPath); length >= 0; length-- {
			if v.dropped[protoPath(oldPath[:length])] {
				dropped = true
				break
			}
		}
		if dropped {
			continue
		}
		for length := len(oldPath); length >= 0; length-- {
			if replacement, exists := v.remapped[protoPath(oldPath[:length])]; exists {
				location.Path = appendPath(replacement, oldPath[length:]...)
				break
			}
		}
		location.ProtoReflect().SetUnknown(nil)
		locations = append(locations, location)
	}
	info.Location = locations
	info.ProtoReflect().SetUnknown(nil)
}
