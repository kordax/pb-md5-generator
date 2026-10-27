package proto

import (
	"bytes"
	"fmt"

	protocompileparser "github.com/bufbuild/protocompile/parser"
	"github.com/bufbuild/protocompile/reporter"
	"github.com/bufbuild/protocompile/sourceinfo"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

// RequestFromSource parses an already bounded local source snapshot. Imports are
// not read, resolved or downloaded; callers supply the same snapshot to the
// annotation parser through ParserOptions.Sources.
func RequestFromSource(name string, source []byte) (*pluginpb.CodeGeneratorRequest, error) {
	handler := reporter.NewHandler(nil)
	file, err := protocompileparser.Parse(name, bytes.NewReader(source), handler)
	if err != nil {
		return nil, fmt.Errorf("parse protobuf source %s: %w", name, err)
	}
	result, err := protocompileparser.ResultFromAST(file, true, handler)
	if err != nil {
		return nil, fmt.Errorf("read protobuf declarations from %s: %w", name, err)
	}
	descriptor := result.FileDescriptorProto()
	descriptor.SourceCodeInfo = sourceinfo.GenerateSourceInfo(file, nil)
	descriptor.PublicDependency = nil
	descriptor.WeakDependency = nil
	descriptors := []*descriptorpb.FileDescriptorProto{descriptor}
	resolveNamedFieldTypes(descriptors)
	return &pluginpb.CodeGeneratorRequest{FileToGenerate: []string{name}, ProtoFile: descriptors}, nil
}
