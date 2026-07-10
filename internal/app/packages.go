package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kordax/pb-md5-generator/engine"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/types/pluginpb"
)

type packageRequest struct {
	name    string
	request *pluginpb.CodeGeneratorRequest
}

func splitRequestByPackage(request *pluginpb.CodeGeneratorRequest) ([]packageRequest, error) {
	descriptors := make(map[string]string, len(request.GetProtoFile()))
	for _, descriptor := range request.GetProtoFile() {
		descriptors[descriptor.GetName()] = descriptor.GetPackage()
	}

	filesByPackage := make(map[string][]string)
	for _, file := range request.GetFileToGenerate() {
		packageName, ok := descriptors[file]
		if !ok {
			return nil, fmt.Errorf("descriptor for source file %s is missing", file)
		}
		filesByPackage[packageName] = append(filesByPackage[packageName], file)
	}

	packageNames := make([]string, 0, len(filesByPackage))
	for packageName := range filesByPackage {
		packageNames = append(packageNames, packageName)
	}
	sort.Strings(packageNames)

	result := make([]packageRequest, 0, len(packageNames))
	for _, packageName := range packageNames {
		files := filesByPackage[packageName]
		sort.Strings(files)
		result = append(result, packageRequest{
			name: packageName,
			request: &pluginpb.CodeGeneratorRequest{
				FileToGenerate:  files,
				Parameter:       request.Parameter,
				ProtoFile:       request.ProtoFile,
				CompilerVersion: request.CompilerVersion,
			},
		})
	}
	return result, nil
}

func writePackageDocuments(
	cfg Config,
	request *pluginpb.CodeGeneratorRequest,
	prefix string,
	style engine.GeneratorStyle,
	deps appDeps,
) error {
	packages, err := splitRequestByPackage(request)
	if err != nil {
		return fmt.Errorf("failed to split protobuf request by package: %w", err)
	}
	if err := deps.mkdirAll(cfg.Output, 0o750); err != nil {
		return fmt.Errorf("failed to initialize markdown output directory %s: %w", cfg.Output, err)
	}
	knownPackages := make([]string, 0, len(packages))
	for _, current := range packages {
		knownPackages = append(knownPackages, current.name)
	}

	for _, current := range packages {
		relativeDir, err := packageDirectory(current.name)
		if err != nil {
			return err
		}
		output := filepath.Join(cfg.Output, relativeDir, "README.md")
		if err := deps.mkdirAll(filepath.Dir(output), 0o750); err != nil {
			return fmt.Errorf("failed to initialize package output directory %s: %w", filepath.Dir(output), err)
		}

		packageTitle := current.name
		if packageTitle == "" {
			packageTitle = "(default)"
		}
		generated, err := deps.generate(current.request, style, current.name, knownPackages)
		if err != nil {
			return fmt.Errorf("failed to generate markdown document for package %s: %w", packageTitle, err)
		}
		log.Info().Msgf("writing package %s to: %s", packageTitle, output)
		if err := deps.writeFile(output, []byte(withTrailingNewline(prefix+generated)), 0o600); err != nil {
			return fmt.Errorf("cannot save package %s to output file %s: %w", packageTitle, output, err)
		}
	}

	indexPath := filepath.Join(cfg.Output, "README.md")
	if err := deps.writeFile(indexPath, []byte(packageIndex(packages)), 0o600); err != nil {
		return fmt.Errorf("cannot save package index to output file %s: %w", indexPath, err)
	}
	return nil
}

func packageDirectory(packageName string) (string, error) {
	if packageName == "" {
		return "_default", nil
	}
	segments := strings.Split(packageName, ".")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || strings.ContainsAny(segment, `/\\`) {
			return "", fmt.Errorf("invalid protobuf package name %q", packageName)
		}
	}
	return filepath.Join(segments...), nil
}

func packageIndex(packages []packageRequest) string {
	var result strings.Builder
	result.WriteString("# Protobuf API Documentation\n\n")
	result.WriteString("Packages:\n\n")
	for _, current := range packages {
		label := current.name
		if label == "" {
			label = "(default)"
		}
		directory, _ := packageDirectory(current.name)
		link := filepath.ToSlash(filepath.Join(directory, "README.md"))
		fmt.Fprintf(&result, "- [`%s`](%s)\n", label, link)
	}
	return withTrailingNewline(result.String())
}

func withTrailingNewline(content string) string {
	return strings.TrimRight(content, "\n") + "\n"
}
