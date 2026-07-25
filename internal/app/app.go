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

const (
	commandGenerate = "generate"
	commandLint     = "lint"
)

type Config struct {
	Command                string
	ProtoDir               string
	ProtoPaths             []string
	Files                  string
	Output                 string
	PrefixDoc              string
	SplitByPackage         bool
	Check                  bool
	Seed                   int64
	StrictAnnotations      bool
	TableIdentifierStyle   string
	HeadingIdentifierStyle string
}

type generationOptions struct {
	style             engine.GeneratorStyle
	packageName       string
	knownPackages     []string
	seed              int64
	strictAnnotations bool
}

type appDeps struct {
	protoFiles       func(string) ([]string, error)
	mkdirAll         func(string, fs.FileMode) error
	requestFromFiles func(Config, []string) (*pluginpb.CodeGeneratorRequest, error)
	readFile         func(string) ([]byte, error)
	writeFile        func(string, []byte, fs.FileMode) error
	generate         func(*pluginpb.CodeGeneratorRequest, generationOptions) (string, error)
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
		log.Err(err).Msg("command failed")
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
	command := commandGenerate
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = args[0]
		args = args[1:]
	}
	if command != commandGenerate && command != commandLint {
		return Config{}, fmt.Errorf("unknown command %q", command)
	}

	flags := flag.NewFlagSet("pb-md5-generator "+command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	cfg := Config{Command: command, Seed: engine.DefaultCodegenSeed}
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
	flags.BoolVar(&cfg.Check, "check", false, "verify generated documentation without writing files")
	flags.Int64Var(&cfg.Seed, "seed", 1, "seed used for deterministic generated examples")
	flags.BoolVar(&cfg.StrictAnnotations, "strict-annotations", false, "reject unknown, duplicate, or misplaced annotations")
	flags.StringVar(&cfg.TableIdentifierStyle, "style-table-identifiers", string(engine.IdentifierStyleBoldCode), "table identifier style: plain, code, bold, bold-code")
	flags.StringVar(&cfg.HeadingIdentifierStyle, "style-heading-identifiers", string(engine.IdentifierStyleCode), "heading identifier style: plain, code, bold, bold-code")

	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	if flags.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if command == commandLint {
		if cfg.Check {
			return Config{}, fmt.Errorf("lint does not support -check")
		}
		cfg.StrictAnnotations = true
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
	cfg, err := normalizeConfig(cfg)
	if err != nil {
		return err
	}
	request, style, err := prepareRequest(cfg, deps)
	if err != nil {
		return err
	}
	options := generationOptions{
		style:             style,
		seed:              cfg.Seed,
		strictAnnotations: cfg.StrictAnnotations || cfg.Command == commandLint,
	}
	if cfg.Command == commandLint {
		return lintRequest(request, options, deps)
	}
	return generateDocuments(cfg, request, options, deps)
}

func normalizeConfig(cfg Config) (Config, error) {
	if cfg.Command == "" {
		cfg.Command = commandGenerate
	}
	if cfg.Command != commandGenerate && cfg.Command != commandLint {
		return Config{}, fmt.Errorf("unknown command %q", cfg.Command)
	}
	if cfg.Command == commandLint && cfg.Check {
		return Config{}, fmt.Errorf("lint does not support -check")
	}
	if cfg.ProtoDir == "" {
		return Config{}, fmt.Errorf("empty proto files directory/string specified")
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
	return cfg, nil
}

func prepareRequest(cfg Config, deps appDeps) (*pluginpb.CodeGeneratorRequest, engine.GeneratorStyle, error) {
	files, err := resolveProtoFiles(cfg, deps)
	if err != nil {
		return nil, engine.GeneratorStyle{}, err
	}
	if len(files) == 0 {
		return nil, engine.GeneratorStyle{}, fmt.Errorf("no files specified")
	}
	if cfg.Command == commandGenerate {
		if err := validatePrefix(cfg.PrefixDoc); err != nil {
			return nil, engine.GeneratorStyle{}, err
		}
	}
	style, err := generatorStyle(cfg)
	if err != nil {
		return nil, engine.GeneratorStyle{}, err
	}
	request, err := deps.requestFromFiles(cfg, files)
	if err != nil {
		return nil, engine.GeneratorStyle{}, fmt.Errorf("failed to generate protobuf request from files: %w", err)
	}
	return request, style, nil
}

func lintRequest(request *pluginpb.CodeGeneratorRequest, options generationOptions, deps appDeps) error {
	if _, err := deps.generate(request, options); err != nil {
		return fmt.Errorf("lint failed: %w", err)
	}
	log.Info().Msg("protobuf documentation annotations are valid")
	return nil
}

func generateDocuments(
	cfg Config,
	request *pluginpb.CodeGeneratorRequest,
	options generationOptions,
	deps appDeps,
) error {
	content, err := prefixContent(cfg.PrefixDoc, deps.readFile)
	if err != nil {
		return err
	}
	if cfg.SplitByPackage {
		return writePackageDocuments(cfg, request, content, options.style, deps)
	}

	generated, err := deps.generate(request, options)
	if err != nil {
		return fmt.Errorf("failed to generate markdown document: %w", err)
	}
	return writeDocument(cfg.Output, content+generated, cfg.Check, deps)
}

func writeDocument(path, content string, check bool, deps appDeps) error {
	expected := withTrailingNewline(content)
	if check {
		actual, err := deps.readFile(path)
		if err != nil {
			return fmt.Errorf("documentation is out of date at %s: %w", path, err)
		}
		if string(actual) != expected {
			return fmt.Errorf("documentation is out of date: %s", path)
		}
		log.Info().Msgf("documentation is up to date: %s", path)
		return nil
	}

	if err := deps.mkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to initialize markdown output directory %s: %w", filepath.Dir(path), err)
	}
	log.Info().Msgf("writing content to: %s", path)
	if err := deps.writeFile(path, []byte(expected), 0o644); err != nil {
		return fmt.Errorf("cannot save results to output file %s: %w", path, err)
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
	return strings.TrimRight(string(contentBytes), "\r\n") + "\n\n", nil
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

func generate(request *pluginpb.CodeGeneratorRequest, options generationOptions) (string, error) {
	parser := engine.NewDescriptorParserWithOptions(request, engine.ParserOptions{
		StrictAnnotations: options.strictAnnotations,
	})
	generator := engine.NewMDGeneratorWithStyle(engine.NewCodegeneratorWithSeed(options.seed), options.style)
	renderer := render.NewMarkdownRenderer(render.DefaultConfig())

	entries, err := parser.Parse()
	if err != nil {
		return "", fmt.Errorf("[parser error] %w", err)
	}
	var document *md.Document
	if options.knownPackages == nil {
		document, err = generator.Generate(entries)
	} else {
		document, err = generator.GeneratePackage(entries, options.packageName, options.knownPackages...)
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
