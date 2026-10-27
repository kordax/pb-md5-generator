package studio

import (
	"context"
	"fmt"

	"github.com/kordax/pb-md5-generator/engine"
	"github.com/kordax/pb-md5-generator/engine/render"
	"github.com/kordax/pb-md5-generator/internal/importer"
	internalproto "github.com/kordax/pb-md5-generator/internal/proto"
	"github.com/kordax/pb-md5-generator/internal/site"
	"google.golang.org/protobuf/types/pluginpb"
)

func compilePublication(ctx context.Context, manifest, output string) error {
	return site.BuildContext(ctx, manifest, output, compilerOptions(engine.DefaultCodegenSeed))
}

func compilerOptions(seed int64) site.Options {
	return site.Options{
		ImportPage: func(ctx context.Context, kind, name string, source []byte, load func(string) ([]byte, error)) (site.ImportResult, error) {
			if err := ctx.Err(); err != nil {
				return site.ImportResult{}, err
			}
			if kind == "openapi" {
				api, err := importer.OpenAPI(ctx, name, source, importer.Load(load))
				return site.ImportResult{API: api}, err
			}
			if kind != "protobuf" {
				return site.ImportResult{}, fmt.Errorf("unsupported page kind %q", kind)
			}
			if err := importer.CheckProtobufSource(ctx, name, source); err != nil {
				return site.ImportResult{}, err
			}
			request, err := internalproto.RequestFromSource(name, source)
			if err != nil {
				return site.ImportResult{}, err
			}
			api, err := importer.ProtobufRequest(ctx, name, request)
			if err != nil {
				return site.ImportResult{}, err
			}
			markdown, err := renderImportedProto(ctx, name, source, request, seed)
			return site.ImportResult{Markdown: []byte(markdown), API: api}, err
		},
	}
}

func renderImportedProto(ctx context.Context, name string, source []byte, request *pluginpb.CodeGeneratorRequest, seed int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	parser := engine.NewDescriptorParserWithOptions(request, engine.ParserOptions{StrictAnnotations: true, Sources: map[string][]byte{name: source}})
	entries, err := parser.Parse()
	if err != nil {
		return "", fmt.Errorf("[parser error] %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	generator := engine.NewMDGeneratorWithStyle(engine.NewCodegeneratorWithSeed(seed), engine.DefaultGeneratorStyle())
	document, err := generator.Generate(entries)
	if err != nil {
		return "", fmt.Errorf("[generator error] %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	result, err := render.NewMarkdownRenderer(render.DefaultConfig()).Render(document)
	if err != nil {
		return "", fmt.Errorf("[renderer error] %w", err)
	}
	return result, ctx.Err()
}
