package app

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/kordax/pb-md5-generator/engine"
	"github.com/kordax/pb-md5-generator/internal/importer"
	internalproto "github.com/kordax/pb-md5-generator/internal/proto"
	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/rs/zerolog/log"
)

func parseSiteFlags(args []string) (Config, error) {
	cfg := Config{Command: commandBuild}
	flags := flag.NewFlagSet("pb-md5-generator build", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&cfg.SiteConfig, "config", "docs.json", "versioned documentation project manifest")
	flags.StringVar(&cfg.Output, "o", "site", "static site output directory")
	flags.StringVar(&cfg.Output, "output", "site", "alias for -o")
	flags.BoolVar(&cfg.Check, "check", false, "verify the complete static site without writing files")
	flags.Int64Var(&cfg.Seed, "seed", engine.DefaultCodegenSeed, "seed used for deterministic protobuf examples")
	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	if flags.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected build arguments: %s", strings.Join(flags.Args(), " "))
	}
	return cfg, nil
}

func buildSite(cfg Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := site.BuildContext(ctx, cfg.SiteConfig, cfg.Output, site.Options{
		Check: cfg.Check,
		ImportPage: func(ctx context.Context, kind, name string, source []byte, load func(string) ([]byte, error)) (site.ImportResult, error) {
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
			markdown, err := generate(request, generationOptions{
				style:             engine.DefaultGeneratorStyle(),
				seed:              cfg.Seed,
				strictAnnotations: true,
				sources:           map[string][]byte{name: source},
			})
			return site.ImportResult{Markdown: []byte(markdown), API: api}, err
		},
	})
	if err != nil {
		return err
	}
	if cfg.Check {
		log.Info().Msgf("site is up to date: %s", cfg.Output)
	} else {
		log.Info().Msgf("site built: %s", cfg.Output)
	}
	return nil
}
