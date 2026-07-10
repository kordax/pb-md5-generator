package proto

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	protocompileparser "github.com/bufbuild/protocompile/parser"
	"github.com/bufbuild/protocompile/reporter"
	"github.com/bufbuild/protocompile/sourceinfo"
	"github.com/kordax/pb-md5-generator/internal/tools"
	goproto "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

// SourceParser reads protobuf syntax directly. It does not resolve imports or
// invoke external programs because documentation only needs source declarations.
type SourceParser struct {
	ProtoDir   string
	ProtoPaths []string
}

func (p SourceParser) RequestFromFiles(files []string) (*pluginpb.CodeGeneratorRequest, error) {
	for _, file := range files {
		if err := tools.RequireRegularFile(file); err != nil {
			return nil, err
		}
	}

	sources, err := p.sourceFiles(files)
	if err != nil {
		return nil, err
	}
	descriptors, err := parseSources(sources)
	if err != nil {
		return nil, err
	}
	resolveNamedFieldTypes(descriptors)

	fileNames := make([]string, 0, len(sources))
	parameters := make([]string, 0, len(sources))
	for _, source := range sources {
		fileNames = append(fileNames, source.name)
		parameters = append(parameters, fmt.Sprintf("M%s=%s", source.name, source.root))
	}
	return &pluginpb.CodeGeneratorRequest{
		FileToGenerate: fileNames,
		Parameter:      goproto.String(strings.Join(parameters, ";")),
		ProtoFile:      descriptors,
	}, nil
}

type sourceFile struct {
	name string
	root string
	path string
}

func parseSources(sources []sourceFile) ([]*descriptorpb.FileDescriptorProto, error) {
	descriptors := make([]*descriptorpb.FileDescriptorProto, 0, len(sources))
	for _, source := range sources {
		file, err := os.Open(source.path) // #nosec G304 -- source paths are supplied by the user.
		if err != nil {
			return nil, fmt.Errorf("open protobuf source %s: %w", source.path, err)
		}

		handler := reporter.NewHandler(nil)
		fileNode, parseErr := protocompileparser.Parse(source.name, file, handler)
		closeErr := file.Close()
		if parseErr != nil {
			return nil, fmt.Errorf("parse protobuf source %s: %w", source.path, parseErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close protobuf source %s: %w", source.path, closeErr)
		}
		result, err := protocompileparser.ResultFromAST(fileNode, true, handler)
		if err != nil {
			return nil, fmt.Errorf("read protobuf declarations from %s: %w", source.path, err)
		}
		descriptor := result.FileDescriptorProto()
		descriptor.SourceCodeInfo = sourceinfo.GenerateSourceInfo(fileNode, nil)
		// protokit only follows public imports. Documentation is intentionally
		// source-local, so imported descriptors are not required.
		descriptor.PublicDependency = nil
		descriptor.WeakDependency = nil
		descriptors = append(descriptors, descriptor)
	}
	return descriptors, nil
}

func (p SourceParser) sourceFiles(files []string) ([]sourceFile, error) {
	roots := p.importPaths()
	result := make([]sourceFile, 0, len(files))
	for _, file := range files {
		absoluteFile, err := filepath.Abs(file)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve proto file %s: %w", file, err)
		}
		matched := false
		for _, root := range roots {
			relative, err := filepath.Rel(root, absoluteFile)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				continue
			}
			result = append(result, sourceFile{
				name: filepath.ToSlash(relative),
				root: root,
				path: absoluteFile,
			})
			matched = true
			break
		}
		if !matched {
			return nil, fmt.Errorf("proto file %s is outside configured proto paths", file)
		}
	}
	return result, nil
}

func (p SourceParser) importPaths() []string {
	configured := append([]string{p.ProtoDir}, p.ProtoPaths...)
	result := make([]string, 0, len(configured))
	seen := make(map[string]struct{}, len(configured))
	for _, value := range configured {
		if value == "" {
			continue
		}
		absolute, err := filepath.Abs(value)
		if err != nil {
			absolute = filepath.Clean(value)
		}
		if _, exists := seen[absolute]; exists {
			continue
		}
		seen[absolute] = struct{}{}
		result = append(result, absolute)
	}
	return result
}

type declarationKind int

const (
	messageDeclaration declarationKind = iota
	enumDeclaration
)

func resolveNamedFieldTypes(files []*descriptorpb.FileDescriptorProto) {
	declarations := make(map[string]declarationKind)
	for _, file := range files {
		prefix := file.GetPackage()
		for _, message := range file.GetMessageType() {
			collectMessageDeclarations(prefix, message, declarations)
		}
		for _, enum := range file.GetEnumType() {
			declarations[joinName(prefix, enum.GetName())] = enumDeclaration
		}
	}
	for _, file := range files {
		for _, message := range file.GetMessageType() {
			resolveMessageFields(joinName(file.GetPackage(), message.GetName()), message, declarations)
		}
	}
}

func collectMessageDeclarations(prefix string, message *descriptorpb.DescriptorProto, declarations map[string]declarationKind) {
	fullName := joinName(prefix, message.GetName())
	declarations[fullName] = messageDeclaration
	for _, nested := range message.GetNestedType() {
		collectMessageDeclarations(fullName, nested, declarations)
	}
	for _, enum := range message.GetEnumType() {
		declarations[joinName(fullName, enum.GetName())] = enumDeclaration
	}
}

func resolveMessageFields(scope string, message *descriptorpb.DescriptorProto, declarations map[string]declarationKind) {
	for _, field := range message.GetField() {
		if field.Type != nil || field.GetTypeName() == "" {
			continue
		}
		resolved, kind := resolveTypeName(field.GetTypeName(), scope, declarations)
		field.TypeName = goproto.String(resolved)
		if kind == enumDeclaration {
			field.Type = descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum()
		} else {
			field.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
		}
	}
	for _, nested := range message.GetNestedType() {
		resolveMessageFields(joinName(scope, nested.GetName()), nested, declarations)
	}
}

func resolveTypeName(raw, scope string, declarations map[string]declarationKind) (string, declarationKind) {
	name := strings.TrimPrefix(raw, ".")
	if strings.HasPrefix(raw, ".") {
		return raw, declarations[name]
	}
	for current := scope; current != ""; current = parentName(current) {
		candidate := joinName(current, name)
		if kind, ok := declarations[candidate]; ok {
			return "." + candidate, kind
		}
	}
	if kind, ok := declarations[name]; ok {
		return "." + name, kind
	}
	if strings.Contains(name, ".") {
		return "." + name, messageDeclaration
	}
	return name, messageDeclaration
}

func joinName(prefix, name string) string {
	if prefix == "" {
		return name
	}
	if name == "" {
		return prefix
	}
	return prefix + "." + name
}

func parentName(name string) string {
	index := strings.LastIndexByte(name, '.')
	if index == -1 {
		return ""
	}
	return name[:index]
}
