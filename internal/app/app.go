package app

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/kordax/pb-md5-generator/engine"
	"github.com/kordax/pb-md5-generator/engine/render"
	internalproto "github.com/kordax/pb-md5-generator/internal/proto"
	"github.com/kordax/pb-md5-generator/internal/tools"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/types/pluginpb"
)

type Config struct {
	ProtoDir               string
	Files                  string
	ProtoOut               string
	Output                 string
	PrefixDoc              string
	TableIdentifierStyle   string
	HeadingIdentifierStyle string
}

type appDeps struct {
	checkDependencies func() error
	protoFiles        func(string) ([]string, error)
	ensureDirectory   func(string) error
	mkdirAll          func(string, fs.FileMode) error
	removeAll         func(string) error
	requestFromFiles  func(Config, []string) (*pluginpb.CodeGeneratorRequest, error)
	readFile          func(string) ([]byte, error)
	writeFile         func(string, []byte, fs.FileMode) error
	generate          func(*pluginpb.CodeGeneratorRequest, engine.GeneratorStyle) (string, error)
}

func Run(args []string) int {
	configureLogger()

	cfg, err := parseFlags(args)
	if err != nil {
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
		checkDependencies: internalproto.CheckDependencies,
		protoFiles:        tools.ProtoFilesRecursively,
		ensureDirectory:   tools.EnsureDirectoryAbsent,
		mkdirAll:          os.MkdirAll,
		removeAll:         os.RemoveAll,
		requestFromFiles: func(cfg Config, files []string) (*pluginpb.CodeGeneratorRequest, error) {
			compiler := internalproto.Compiler{ProtoDir: cfg.ProtoDir, OutputDir: cfg.ProtoOut}
			return compiler.RequestFromFiles(files)
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
	flags.StringVar(&cfg.ProtoDir, "d", "", ".proto files directory, e.g.: ./test/test-protos")
	flags.StringVar(&cfg.Files, "f", "", "force specific files, e.g.: ./test/my-proto.proto;./test/my-next-proto.proto")
	flags.StringVar(&cfg.ProtoOut, "pbo", "doc-generator-tmp", "temporary protobuf output directory location")
	flags.StringVar(&cfg.Output, "o", "./doc-generator-output", "markdown output file")
	flags.StringVar(&cfg.PrefixDoc, "p", "", "prefix markdown document file that will be added to the beginning of the resulting .md file")
	flags.StringVar(&cfg.TableIdentifierStyle, "style-table-identifiers", string(engine.IdentifierStyleBoldCode), "table identifier style: plain, code, bold, bold-code")
	flags.StringVar(&cfg.HeadingIdentifierStyle, "style-heading-identifiers", string(engine.IdentifierStyleCode), "heading identifier style: plain, code, bold, bold-code")

	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func run(cfg Config) error {
	return runWithDeps(cfg, defaultDeps())
}

func runWithDeps(cfg Config, deps appDeps) error {
	if err := deps.checkDependencies(); err != nil {
		return err
	}
	if cfg.ProtoDir == "" {
		return fmt.Errorf("empty proto files directory/string specified")
	}

	cfg.ProtoDir = path.Clean(cfg.ProtoDir)
	cfg.ProtoOut = path.Clean(cfg.ProtoOut)
	cfg.Output = normalizedMarkdownOutput(cfg.Output)

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
	if err := deps.ensureDirectory(cfg.ProtoOut); err != nil {
		return fmt.Errorf("temporary protobuf directory '%s' is not available: %w", cfg.ProtoOut, err)
	}
	if err := deps.mkdirAll(cfg.ProtoOut, 0o750); err != nil {
		return fmt.Errorf("failed to initialize output directory %s: %w", cfg.ProtoOut, err)
	}
	defer cleanup(cfg.ProtoOut, deps.removeAll)

	request, err := deps.requestFromFiles(cfg, files)
	if err != nil {
		return fmt.Errorf("failed to generate protobuf request from files: %w", err)
	}

	content, err := prefixContent(cfg.PrefixDoc, deps.readFile)
	if err != nil {
		return err
	}
	generated, err := deps.generate(request, style)
	if err != nil {
		return fmt.Errorf("failed to generate markdown document: %w", err)
	}
	content += generated

	log.Info().Msgf("writing content to: %s", cfg.Output)
	if err := deps.writeFile(cfg.Output, []byte(content), 0o600); err != nil {
		return fmt.Errorf("cannot save results to output directory %s: %w", cfg.Output, err)
	}
	return nil
}

func configureLogger() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: "02/01 15:04:05"})
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
}

func resolveProtoFiles(cfg Config, deps appDeps) ([]string, error) {
	if cfg.Files != "" {
		return strings.Split(cfg.Files, ";"), nil
	}
	files, err := deps.protoFiles(cfg.ProtoDir)
	if err != nil {
		return nil, fmt.Errorf("failed to list .proto files: %w", err)
	}
	return files, nil
}

func normalizedMarkdownOutput(output string) string {
	output = path.Clean(output)
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

func generate(request *pluginpb.CodeGeneratorRequest, style engine.GeneratorStyle) (string, error) {
	parser := engine.NewDescriptorParser(request)
	generator := engine.NewMDGeneratorWithStyle(engine.NewCodegenerator(), style)
	renderer := render.NewMarkdownRenderer(render.DefaultConfig())

	entries, err := parser.Parse()
	if err != nil {
		return "", fmt.Errorf("[parser error] %w", err)
	}
	document, err := generator.Generate(entries)
	if err != nil {
		return "", fmt.Errorf("[generator error] %w", err)
	}
	content, err := renderer.Render(document)
	if err != nil {
		return "", fmt.Errorf("[renderer error] %w", err)
	}
	return content, nil
}

func cleanup(directory string, removeAll func(string) error) {
	if err := removeAll(directory); err != nil {
		log.Warn().Err(err).Msg("failed to cleanup tmp directory")
	}
}
