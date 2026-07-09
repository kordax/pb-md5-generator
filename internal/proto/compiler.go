package proto

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	plugingo "github.com/golang/protobuf/protoc-gen-go/plugin"
	"github.com/kordax/pb-md5-generator/internal/tools"
	"github.com/rs/zerolog/log"
	goproto "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

const descriptorFilename = "protobuf.desc"

type Compiler struct {
	ProtoDir  string
	OutputDir string
}

func CheckDependencies() error {
	if _, err := exec.LookPath("protoc"); err != nil {
		return fmt.Errorf("protoc binary is required: %w", err)
	}
	return nil
}

func (c Compiler) RequestFromFiles(files []string) (*plugingo.CodeGeneratorRequest, error) {
	for _, file := range files {
		if err := tools.RequireRegularFile(file); err != nil {
			return nil, err
		}
	}

	protos, parameters, err := c.Compile(files)
	if err != nil {
		return nil, err
	}

	fileNames := make([]string, 0, len(files))
	for _, file := range files {
		fileNames = append(fileNames, path.Base(file))
	}

	return &plugingo.CodeGeneratorRequest{
		FileToGenerate:  fileNames,
		Parameter:       goproto.String(strings.Join(parameters, ";")),
		ProtoFile:       protos,
		CompilerVersion: nil,
	}, nil
}

func (c Compiler) Compile(files []string) ([]*descriptorpb.FileDescriptorProto, []string, error) {
	parameters := make([]string, 0, len(files))
	fileNames := make([]string, 0, len(files))
	args := []string{"--proto_path=" + c.ProtoDir}

	for _, file := range files {
		fileName := path.Base(file)
		fileNames = append(fileNames, fileName)
		parameter := fmt.Sprintf("M%s=%s", fileName, c.ProtoDir)
		parameters = append(parameters, parameter)
		args = append(args, "--go_opt="+parameter)
	}

	descFile := filepath.Join(c.OutputDir, descriptorFilename)
	args = append(args,
		"--descriptor_set_out="+descFile,
		"--include_source_info",
	)
	args = append(args, fileNames...)
	args = append(args, "--go_out="+c.OutputDir)

	cmd := exec.Command("protoc", args...) // #nosec G204 -- protoc arguments are built from validated CLI file paths.
	log.Trace().Msgf("executing cmd: protoc %s", strings.Join(args, " "))

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			log.Error().Err(err).Msgf("`protoc` error, return code: %d", exitError.ExitCode())
			log.Error().Msgf("stderr: %s", stderr.String())
		} else {
			log.Error().Err(err).Msg("`protoc` error")
		}
		log.Error().Msgf("output: %s", string(out))
		return nil, nil, err
	}

	readFile, err := os.ReadFile(descFile) // #nosec G304 -- descriptor path is created in the configured temporary output directory.
	if err != nil {
		return nil, nil, err
	}

	fds := &descriptorpb.FileDescriptorSet{}
	if err := goproto.Unmarshal(readFile, fds); err != nil {
		return nil, nil, err
	}

	return fds.File, parameters, nil
}
