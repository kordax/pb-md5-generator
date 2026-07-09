package app

import (
	"flag"
	"fmt"
	"os"
	"path"
	"strings"

	plugingo "github.com/golang/protobuf/protoc-gen-go/plugin"
	"github.com/kordax/pb-md5-generator/engine"
	"github.com/kordax/pb-md5-generator/engine/render"
	internalproto "github.com/kordax/pb-md5-generator/internal/proto"
	"github.com/kordax/pb-md5-generator/internal/tools"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type Config struct {
	ProtoDir  string
	Files     string
	ProtoOut  string
	Output    string
	PrefixDoc string
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

func parseFlags(args []string) (Config, error) {
	flags := flag.NewFlagSet("pb-md5-generator", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	cfg := Config{}
	flags.StringVar(&cfg.ProtoDir, "d", "", ".proto files directory, e.g.: ./test/test-protos")
	flags.StringVar(&cfg.Files, "f", "", "force specific files, e.g.: ./test/my-proto.proto;./test/my-next-proto.proto")
	flags.StringVar(&cfg.ProtoOut, "pbo", "doc-generator-tmp", "temporary protobuf output directory location")
	flags.StringVar(&cfg.Output, "o", "./doc-generator-output", "markdown output file")
	flags.StringVar(&cfg.PrefixDoc, "p", "", "prefix markdown document file that will be added to the beginning of the resulting .md file")

	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func run(cfg Config) error {
	if err := internalproto.CheckDependencies(); err != nil {
		return err
	}
	if cfg.ProtoDir == "" {
		return fmt.Errorf("empty proto files directory/string specified")
	}

	cfg.ProtoDir = path.Clean(cfg.ProtoDir)
	cfg.ProtoOut = path.Clean(cfg.ProtoOut)
	cfg.Output = normalizedMarkdownOutput(cfg.Output)

	files, err := resolveProtoFiles(cfg)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no files specified")
	}
	if err := validatePrefix(cfg.PrefixDoc); err != nil {
		return err
	}
	if err := tools.EnsureDirectoryAbsent(cfg.ProtoOut); err != nil {
		return fmt.Errorf("temporary protobuf directory '%s' is not available: %w", cfg.ProtoOut, err)
	}
	if err := os.MkdirAll(cfg.ProtoOut, 0o750); err != nil {
		return fmt.Errorf("failed to initialize output directory %s: %w", cfg.ProtoOut, err)
	}
	defer cleanup(cfg.ProtoOut)

	compiler := internalproto.Compiler{ProtoDir: cfg.ProtoDir, OutputDir: cfg.ProtoOut}
	request, err := compiler.RequestFromFiles(files)
	if err != nil {
		return fmt.Errorf("failed to generate protobuf request from files: %w", err)
	}

	content, err := prefixContent(cfg.PrefixDoc)
	if err != nil {
		return err
	}
	generated, err := generate(request)
	if err != nil {
		return fmt.Errorf("failed to generate markdown document: %w", err)
	}
	content += generated

	log.Info().Msgf("writing content to: %s", cfg.Output)
	if err := os.WriteFile(cfg.Output, []byte(content), 0o600); err != nil {
		return fmt.Errorf("cannot save results to output directory %s: %w", cfg.Output, err)
	}
	return nil
}

func configureLogger() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: "02/01 15:04:05"})
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
}

func resolveProtoFiles(cfg Config) ([]string, error) {
	if cfg.Files != "" {
		return strings.Split(cfg.Files, ";"), nil
	}
	files, err := tools.ProtoFilesRecursively(cfg.ProtoDir)
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

func prefixContent(prefix string) (string, error) {
	if prefix == "" {
		return "", nil
	}
	contentBytes, err := os.ReadFile(prefix) // #nosec G304 -- prefix is an explicit CLI input.
	if err != nil {
		return "", fmt.Errorf("failed to read prefix markdown document %s: %w", prefix, err)
	}
	return string(contentBytes) + "\n\n", nil
}

func generate(request *plugingo.CodeGeneratorRequest) (string, error) {
	parser := engine.NewDescriptorParser(request)
	generator := engine.NewMDGenerator(engine.NewCodegenerator())
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

func cleanup(directory string) {
	if err := os.RemoveAll(directory); err != nil {
		log.Warn().Err(err).Msg("failed to cleanup tmp directory")
	}
}
