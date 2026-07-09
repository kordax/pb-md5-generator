//go:build integration_test

package pb_md5_generator_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCLIIntegrationGeneratesMarkdownForRealAPI(t *testing.T) {
	requireTool(t, "protoc")
	requireTool(t, "protoc-gen-go")

	root := t.TempDir()
	protoDir := filepath.Join(root, "proto")
	require.NoError(t, os.Mkdir(protoDir, 0o750))
	writeFile(t, filepath.Join(protoDir, "api.proto"), `syntax = "proto3";
package fixture;
option go_package = "./fixture";

// @title: Fixture API
// @header: Users
enum Status {
  STATUS_UNKNOWN = 0;
  STATUS_ACTIVE = 1;
}

/*
 * Creates a user.
 * @autocode[json]
 */
message CreateUserRequest {
  // @type=email
  string email = 1;
  // @min=18
  // @max=100
  int32 age = 2;
  bool enabled = 3;
}
`)
	prefix := filepath.Join(root, "prefix.md")
	writeFile(t, prefix, "# Prefix")
	output := filepath.Join(root, "out.md")

	run := runCLI(t,
		"-d", protoDir,
		"-pbo", filepath.Join(root, "tmp-pb"),
		"-o", output,
		"-p", prefix,
	)
	require.NoError(t, run.err, run.output)

	result, err := os.ReadFile(output)
	require.NoError(t, err)
	markdown := string(result)
	assert.Contains(t, markdown, "# Prefix")
	assert.Contains(t, markdown, "Fixture API")
	assert.Contains(t, markdown, "CreateUserRequest")
	assert.Contains(t, markdown, "email")
	assert.Contains(t, markdown, "Enums")
	assert.Contains(t, markdown, "STATUS_ACTIVE")
}

func TestCLIIntegrationReportsFieldLocation(t *testing.T) {
	requireTool(t, "protoc")
	requireTool(t, "protoc-gen-go")

	root := t.TempDir()
	protoDir := filepath.Join(root, "proto")
	require.NoError(t, os.Mkdir(protoDir, 0o750))
	writeFile(t, filepath.Join(protoDir, "api.proto"), `syntax = "proto3";
package fixture;
option go_package = "./fixture";

message BrokenRequest {
  // @type=not_real
  string name = 1;
}
`)

	run := runCLI(t,
		"-d", protoDir,
		"-pbo", filepath.Join(root, "tmp-pb"),
		"-o", filepath.Join(root, "out.md"),
	)
	require.Error(t, run.err)
	assert.Contains(t, run.output, "api.proto:7")
	assert.Contains(t, run.output, "field name")
	assert.Contains(t, run.output, "unknown custom type provided: not_real")
}

func TestCLIIntegrationReportsCodeMarkerLocation(t *testing.T) {
	requireTool(t, "protoc")
	requireTool(t, "protoc-gen-go")

	root := t.TempDir()
	protoDir := filepath.Join(root, "proto")
	require.NoError(t, os.Mkdir(protoDir, 0o750))
	writeFile(t, filepath.Join(protoDir, "api.proto"), `syntax = "proto3";
package fixture;
option go_package = "./fixture";

/*
 * @code[json]:
 * {"broken":
 */
message BrokenCode {
  string name = 1;
}
`)

	run := runCLI(t,
		"-d", protoDir,
		"-pbo", filepath.Join(root, "tmp-pb"),
		"-o", filepath.Join(root, "out.md"),
	)
	require.Error(t, run.err)
	assert.Contains(t, run.output, "api.proto:6")
	assert.Contains(t, run.output, "message BrokenCode marker @code")
	assert.Contains(t, run.output, "failed to marshal and validate json code")
}

type cliRun struct {
	output string
	err    error
}

func runCLI(t *testing.T, args ...string) cliRun {
	t.Helper()
	cmdArgs := append([]string{"run", "./cmd/pb-md5-generator"}, args...)
	cmd := exec.Command("go", cmdArgs...)
	out, err := cmd.CombinedOutput()
	return cliRun{output: string(out), err: err}
}

func requireTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s is required for CLI integration tests", name)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o600))
}
