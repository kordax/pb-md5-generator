package app

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kordax/pb-md5-generator/engine"
	"github.com/kordax/pb-md5-generator/engine/md"
	"github.com/kordax/pb-md5-generator/engine/render"
	internalproto "github.com/kordax/pb-md5-generator/internal/proto"
	"github.com/kordax/pb-md5-generator/internal/tools"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/types/pluginpb"
)

type Config struct {
	ProtoDir               string
	ProtoPaths             []string
	Files                  string
	Output                 string
	PrefixDoc              string
	SplitByPackage         bool
	TableIdentifierStyle   string
	HeadingIdentifierStyle string
}

type appDeps struct {
	protoFiles       func(string) ([]string, error)
	mkdirAll         func(string, fs.FileMode) error
	requestFromFiles func(Config, []string) (*pluginpb.CodeGeneratorRequest, error)
	readFile         func(string) ([]byte, error)
	writeFile        func(string, []byte, fs.FileMode) error
	generate         func(*pluginpb.CodeGeneratorRequest, engine.GeneratorStyle, string, []string) (string, error)
}

func Run(args []string) int {
	configureLogger()

	cfg, err := parseFlags(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		log.Err(err).Msg("failed to parse flags")
		return 1
	}
	if err := run(cfg); err != nil {
		log.Err(err).Msg("generation failed")
		return 1
	}
	return 0
}

func defaultDeps() appDeps {
	return appDeps{
		protoFiles: tools.ProtoFilesRecursively,
		mkdirAll:   os.MkdirAll,
		requestFromFiles: func(cfg Config, files []string) (*pluginpb.CodeGeneratorRequest, error) {
			parser := internalproto.SourceParser{
				ProtoDir:   cfg.ProtoDir,
				ProtoPaths: cfg.ProtoPaths,
			}
			return parser.RequestFromFiles(files)
		},
		readFile:  os.ReadFile,
		writeFile: os.WriteFile,
		generate:  generate,
	}
}

func parseFlags(args []string) (Config, error) {
	flags := flag.NewFlagSet("pb-md5-generator", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	cfg := Config{}
	protoPaths := &stringListFlag{}
	flags.StringVar(&cfg.ProtoDir, "d", "", ".proto files directory, e.g.: ./test/test-protos")
	flags.StringVar(&cfg.ProtoDir, "proto-dir", "", "alias for -d")
	flags.Var(protoPaths, "I", "additional local protobuf source root; may be repeated")
	flags.Var(protoPaths, "proto-path", "alias for -I; may be repeated")
	flags.StringVar(&cfg.Files, "f", "", "force specific files, e.g.: ./test/my-proto.proto;./test/my-next-proto.proto")
	flags.StringVar(&cfg.Files, "files", "", "alias for -f")
	flags.StringVar(&cfg.Output, "o", "./doc-generator-output", "markdown output file")
	flags.StringVar(&cfg.Output, "output", "./doc-generator-output", "alias for -o")
	flags.StringVar(&cfg.PrefixDoc, "p", "", "prefix markdown document file that will be added to the beginning of the resulting .md file")
	flags.BoolVar(&cfg.SplitByPackage, "split-by-package", false, "write <output>/<package path>/README.md for every protobuf package")
	flags.StringVar(&cfg.TableIdentifierStyle, "style-table-identifiers", string(engine.IdentifierStyleBoldCode), "table identifier style: plain, code, bold, bold-code")
	flags.StringVar(&cfg.HeadingIdentifierStyle, "style-heading-identifiers", string(engine.IdentifierStyleCode), "heading identifier style: plain, code, bold, bold-code")

	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	cfg.ProtoPaths = protoPaths.values
	return cfg, nil
}

type stringListFlag struct {
	values []string
}

func (f *stringListFlag) String() string {
	return strings.Join(f.values, string(os.PathListSeparator))
}

func (f *stringListFlag) Set(value string) error {
	for _, item := range filepath.SplitList(value) {
		item = strings.TrimSpace(item)
		if item != "" {
			f.values = append(f.values, item)
		}
	}
	return nil
}

func run(cfg Config) error {
	return runWithDeps(cfg, defaultDeps())
}

func runWithDeps(cfg Config, deps appDeps) error {
	if cfg.ProtoDir == "" {
		return fmt.Errorf("empty proto files directory/string specified")
	}

	cfg.ProtoDir = filepath.Clean(cfg.ProtoDir)
	for i := range cfg.ProtoPaths {
		cfg.ProtoPaths[i] = filepath.Clean(cfg.ProtoPaths[i])
	}
	if cfg.SplitByPackage {
		cfg.Output = filepath.Clean(cfg.Output)
	} else {
		cfg.Output = normalizedMarkdownOutput(cfg.Output)
	}

	files, err := resolveProtoFiles(cfg, deps)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no files specified")
	}
	if err := validatePrefix(cfg.PrefixDoc); err != nil {
		return err
	}
	style, err := generatorStyle(cfg)
	if err != nil {
		return err
	}
	request, err := deps.requestFromFiles(cfg, files)
	if err != nil {
		return fmt.Errorf("failed to generate protobuf request from files: %w", err)
	}

	content, err := prefixContent(cfg.PrefixDoc, deps.readFile)
	if err != nil {
		return err
	}
	if cfg.SplitByPackage {
		return writePackageDocuments(cfg, request, content, style, deps)
	}

	generated, err := deps.generate(request, style, "", nil)
	if err != nil {
		return fmt.Errorf("failed to generate markdown document: %w", err)
	}
	content += generated

	if err := deps.mkdirAll(filepath.Dir(cfg.Output), 0o755); err != nil {
		return fmt.Errorf("failed to initialize markdown output directory %s: %w", filepath.Dir(cfg.Output), err)
	}
	log.Info().Msgf("writing content to: %s", cfg.Output)
	if err := deps.writeFile(cfg.Output, []byte(withTrailingNewline(content)), 0o644); err != nil {
		return fmt.Errorf("cannot save results to output file %s: %w", cfg.Output, err)
	}
	return nil
}

func configureLogger() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: "02/01 15:04:05"})
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
}

func resolveProtoFiles(cfg Config, deps appDeps) ([]string, error) {
	if cfg.Files != "" {
		values := strings.Split(cfg.Files, ";")
		files := make([]string, 0, len(values))
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			files = append(files, resolveProtoFile(value, cfg))
		}
		return files, nil
	}
	files, err := deps.protoFiles(cfg.ProtoDir)
	if err != nil {
		return nil, fmt.Errorf("failed to list .proto files: %w", err)
	}
	return files, nil
}

func resolveProtoFile(file string, cfg Config) string {
	file = filepath.Clean(file)
	if filepath.IsAbs(file) {
		return file
	}
	if stat, err := os.Stat(file); err == nil && !stat.IsDir() {
		return file
	}
	for _, root := range append([]string{cfg.ProtoDir}, cfg.ProtoPaths...) {
		candidate := filepath.Join(root, file)
		if stat, err := os.Stat(candidate); err == nil && !stat.IsDir() {
			return candidate
		}
	}
	return file
}

func normalizedMarkdownOutput(output string) string {
	output = filepath.Clean(output)
	if strings.HasSuffix(output, ".md") {
		return output
	}
	return output + ".md"
}

func validatePrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	stat, err := os.Stat(prefix)
	if err != nil {
		return fmt.Errorf("prefix document '%s' doesn't exist: %w", prefix, err)
	}
	if stat.IsDir() {
		return fmt.Errorf("prefix document '%s' path is a directory", prefix)
	}
	return nil
}

func prefixContent(prefix string, readFile func(string) ([]byte, error)) (string, error) {
	if prefix == "" {
		return "", nil
	}
	contentBytes, err := readFile(prefix)
	if err != nil {
		return "", fmt.Errorf("failed to read prefix markdown document %s: %w", prefix, err)
	}
	return string(contentBytes) + "\n\n", nil
}

func generatorStyle(cfg Config) (engine.GeneratorStyle, error) {
	defaults := engine.DefaultGeneratorStyle()
	tableIdentifierStyle := cfg.TableIdentifierStyle
	if tableIdentifierStyle == "" {
		tableIdentifierStyle = string(defaults.TableIdentifiers)
	}
	headingIdentifierStyle := cfg.HeadingIdentifierStyle
	if headingIdentifierStyle == "" {
		headingIdentifierStyle = string(defaults.HeadingIdentifiers)
	}

	tableIdentifiers, err := engine.ParseIdentifierStyle(tableIdentifierStyle)
	if err != nil {
		return engine.GeneratorStyle{}, fmt.Errorf("invalid table identifier style: %w", err)
	}
	headingIdentifiers, err := engine.ParseIdentifierStyle(headingIdentifierStyle)
	if err != nil {
		return engine.GeneratorStyle{}, fmt.Errorf("invalid heading identifier style: %w", err)
	}
	return engine.GeneratorStyle{
		TableIdentifiers:   tableIdentifiers,
		HeadingIdentifiers: headingIdentifiers,
	}, nil
}

func generate(
	request *pluginpb.CodeGeneratorRequest,
	style engine.GeneratorStyle,
	packageName string,
	knownPackages []string,
) (string, error) {
	parser := engine.NewDescriptorParser(request)
	generator := engine.NewMDGeneratorWithStyle(engine.NewCodegenerator(), style)
	renderer := render.NewMarkdownRenderer(render.DefaultConfig())

	entries, err := parser.Parse()
	if err != nil {
		return "", fmt.Errorf("[parser error] %w", err)
	}
	var document *md.Document
	if knownPackages == nil {
		document, err = generator.Generate(entries)
	} else {
		document, err = generator.GeneratePackage(entries, packageName, knownPackages...)
	}
	if err != nil {
		return "", fmt.Errorf("[generator error] %w", err)
	}
	content, err := renderer.Render(document)
	if err != nil {
		return "", fmt.Errorf("[renderer error] %w", err)
	}
	return content, nil
}
