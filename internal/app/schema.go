package app

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kordax/pb-md5-generator/document"
)

func parseSchemaFlags(args []string) (Config, error) {
	cfg := Config{Command: commandSchema}
	flags := flag.NewFlagSet("pb-md5-generator schema", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&cfg.SchemaKind, "kind", "manifest", "schema kind: manifest, project or page")
	flags.StringVar(&cfg.Output, "o", "-", "output JSON file, or - for stdout")
	flags.StringVar(&cfg.Output, "output", "-", "alias for -o")
	flags.BoolVar(&cfg.Check, "check", false, "compare an existing schema file without writing")
	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	if flags.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected schema arguments: %s", strings.Join(flags.Args(), " "))
	}
	if cfg.Check && cfg.Output == "-" {
		return Config{}, fmt.Errorf("schema -check requires an output file")
	}
	return cfg, nil
}

func writeSchema(cfg Config) error { return writeSchemaTo(cfg, os.Stdout) }

func writeSchemaTo(cfg Config, stdout io.Writer) error {
	data, err := document.JSONSchema(cfg.SchemaKind)
	if err != nil {
		return err
	}
	if cfg.Output == "-" {
		if cfg.Check {
			return fmt.Errorf("schema -check requires an output file")
		}
		_, err := stdout.Write(data)
		return err
	}
	if cfg.Output == "" {
		return fmt.Errorf("schema output is required")
	}
	return writeDocument(cfg.Output, string(data), cfg.Check, defaultDeps())
}
