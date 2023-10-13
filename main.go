package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	plugingo "github.com/golang/protobuf/protoc-gen-go/plugin"
	"github.com/kordax/pb-md5-generator/engine"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	arrayutils "gitlab.com/kordax/basic-utils/array-utils"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

const pbDescName = "protobuf.desc"

var dir = flag.String("d", "", ".proto files directory, e.g.: ./test/test-protos")
var file = flag.String("f", "", "force specific files, e.g.: ./test/my-proto.proto;./test/my-next-proto.proto")
var pbOutput = flag.String("pbo", "doc-generator-tmp", "temporary protobuf output directory location")
var output = flag.String("o", "./doc-generator-output", "markdown output file")
var prefix = flag.String("p", "", "prefix markdown document file that will be added to the beginning of the resulting .md file")

func main() {
	flag.Parse()
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: "02/01 15:04:05"})
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	checkDependencies()

	if *dir == "" {
		log.Error().Msg("empty proto files directory/string specified")
		os.Exit(1)
	}

	var files []string
	if *file != "" {
		files = strings.Split(*file, ";")
	} else {
		var err error
		*dir = path.Clean(*dir)
		files, err = getProtoFilesRecursively(*dir)
		if err != nil {
			log.Err(err).Msg("failed to list .proto files")
			os.Exit(2)
		}
	}
	if len(files) == 0 {
		log.Error().Msg("no files specified")
		os.Exit(1)
	}

	*output = path.Clean(*output)
	*pbOutput = path.Clean(*pbOutput)

	if !strings.HasSuffix(*output, ".md") {
		*output = *output + ".md"
	}

	if *prefix != "" {
		if stat, err := os.Stat(*prefix); err != nil {
			log.Err(err).Msgf("prefix document '%s' doesn't exist", *prefix)
			os.Exit(3)
		} else {
			if stat.IsDir() {
				log.Err(err).Msgf("prefix document '%s' path is a directory", *prefix)
				os.Exit(4)
			}
		}
	}

	if stat, err := os.Stat(*pbOutput); err == nil {
		if stat.IsDir() {
			log.Err(err).Msgf("temporary protobuf directory '%s' already exists", *pbOutput)
			os.Exit(5)
		}
	}

	err := os.MkdirAll(*pbOutput, os.ModePerm)
	if err != nil {
		log.Err(err).Msgf("failed to initialize output directory: %s", *pbOutput)
		os.Exit(6)
	}
	defer func(path string) {
		remerr := os.RemoveAll(path)
		if remerr != nil {
			log.Warn().Err(remerr).Msgf("failed to cleanup tmp directory")
		}
	}(*pbOutput)

	request, err := requestFromFiles(files)
	if err != nil {
		log.Err(err).Msg("failed to generate protobuf request from files")
		os.Exit(7)
	}

	contentBytes, err := os.ReadFile(*prefix)
	if err != nil {
		log.Err(err).Msgf("failed to read prefix markdown document: %s", *prefix)
		os.Exit(8)
	}
	content := string(contentBytes) + "\n\n"
	generated, err := generate(request)
	if err != nil {
		log.Err(err).Msg("failed to generate markdown document")
		os.Exit(9)
	}
	content += generated

	log.Info().Msgf("writing content to: %s", *output)
	err = os.WriteFile(*output, []byte(content), 0644)
	if err != nil {
		log.Err(err).Msgf("cannot save results to output directory: %s", *output)
		os.Exit(10)
	}
}

func checkDependencies() {
	cmd := bash("which protoc")
	if out, err := cmd.Output(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			log.Error().Err(err).Msgf("failed to check `protoc` binary. protoc should be available, return code: %d", exitError.ExitCode())
		} else {
			log.Error().Err(err).Msg("failed to check `protoc` binary. protoc should be available.")
		}

		log.Error().Msgf("output: %s", string(out))
		panic(err)
	}
}

func requestFromFiles(files []string) (*plugingo.CodeGeneratorRequest, error) {
	for _, file := range files {
		stat, err := os.Stat(file)
		if err != nil {
			return nil, fmt.Errorf("cannot process file %s: %s", file, err.Error())
		}

		if stat.IsDir() {
			return nil, fmt.Errorf("invalid path specified, path is a dir: %s", file)
		}
	}

	protos, parameters, err := protoc(files)
	if err != nil {
		return nil, err
	}

	return &plugingo.CodeGeneratorRequest{
		FileToGenerate:  arrayutils.Map(files, func(v *string) string { return path.Base(*v) }),
		Parameter:       proto.String(strings.Join(parameters, ";")),
		ProtoFile:       protos,
		CompilerVersion: nil,
	}, nil
}

func protoc(files []string) ([]*descriptorpb.FileDescriptorProto, []string, error) {
	parameters := make([]string, 0)
	command := "protoc --proto_path=" + *dir + " "
	var fileNames []string
	for _, file := range files {
		fileName := path.Base(file)
		fileNames = append(fileNames, fileName)
		mParam := fmt.Sprintf("M%s=%s", fileName, *dir)
		command += fmt.Sprintf("--go_opt=M%s=%s ", fileName, *dir)
		parameters = append(parameters, mParam)
	}
	arrayutils.Map(files, func(v *string) string {
		return path.Base(*v)
	})

	descFile := *pbOutput + "/" + pbDescName
	command += "--descriptor_set_out=" + descFile + " "
	command += "--include_source_info "
	command += strings.Join(fileNames, " ") + " "
	command += "--go_out=" + *pbOutput

	cmd := bash(command)
	if out, err := cmd.Output(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			log.Error().Err(err).Msgf("`protoc` error, return code: %d", exitError.ExitCode())
			log.Error().Msgf("stderr: %s", string(exitError.Stderr))
		} else {
			log.Error().Err(err).Msg("`protoc` error.")
		}

		log.Error().Msgf("output: %s", out)
		return nil, nil, err
	}

	var result []*descriptorpb.FileDescriptorProto
	readFile, err := os.ReadFile(descFile)
	if err != nil {
		return nil, nil, err
	}

	fds := &descriptorpb.FileDescriptorSet{}
	err = proto.Unmarshal(readFile, fds)
	if err != nil {
		return nil, nil, err
	}
	result = append(result, fds.File...)

	return result, parameters, nil
}

func generate(request *plugingo.CodeGeneratorRequest) (string, error) {
	var content string

	parser := engine.NewDescriptorParser(request)
	generator := engine.NewMDGenerator(engine.NewCodegenerator())
	renderer := engine.NewMarkdownRenderer(engine.DefaultRenderConfig())
	entries, err := parser.Parse()
	if err != nil {
		return "", fmt.Errorf("[parser error] %s", err.Error())
	}

	document, err := generator.Generate(entries)
	if err != nil {
		return "", fmt.Errorf("[generator error] %s", err.Error())
	}

	content, err = renderer.Render(document)
	if err != nil {
		return "", fmt.Errorf("[renderer error] %s", err.Error())
	}

	return content, nil
}

func bash(cmd string) *exec.Cmd {
	log.Trace().Msgf("executing cmd: %s", cmd)
	return exec.Command("/usr/bin/bash", "-c", cmd)
}

func getProtoFilesRecursively(directory string) ([]string, error) {
	var result []string
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !entry.IsDir() {
			if filepath.Ext(entry.Name()) == ".proto" {
				result = append(result, path)
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return result, nil
}
