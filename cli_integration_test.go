//go:build integration_test

package pb_md5_generator_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kordax/basic-utils/v3/ufile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCLIIntegrationGeneratesMarkdownForRealAPI(t *testing.T) {
	root := t.TempDir()
	protoDir := copyFixture(t, root, "full_api.proto")
	prefix := filepath.Join(root, "prefix.md")
	ufile.MustWrite(prefix, []byte("# Prefix\n"), 0o600)
	output := filepath.Join(root, "out.md")

	run := runCLI(t,
		"-d", protoDir,
		"-o", output,
		"-p", prefix,
	)
	require.NoError(t, run.err, run.output)

	markdown := string(ufile.MustRead(output))
	assert.Contains(t, markdown, "# Prefix")
	assert.Contains(t, markdown, "Fixture API")
	assert.Contains(t, markdown, "Users")
	assert.Contains(t, markdown, "Sessions")
	assert.Contains(t, markdown, "Billing")
	assert.Contains(t, markdown, "Inventory")
	assert.Contains(t, markdown, "Audit")
	assert.Contains(t, markdown, "Notifications")
	assert.Contains(t, markdown, "CreateUserRequest")
	assert.Contains(t, markdown, "CreateUserResponse")
	assert.Contains(t, markdown, "SessionResponse")
	assert.Contains(t, markdown, "SessionAuditRequest")
	assert.Contains(t, markdown, "Profile")
	assert.Contains(t, markdown, "Address")
	assert.Contains(t, markdown, "GeoPoint")
	assert.Contains(t, markdown, "Preferences")
	assert.Contains(t, markdown, "CreatePaymentRequest")
	assert.Contains(t, markdown, "CreatePaymentResponse")
	assert.Contains(t, markdown, "RefundPaymentRequest")
	assert.Contains(t, markdown, "SearchCatalogRequest")
	assert.Contains(t, markdown, "InventorySnapshot")
	assert.Contains(t, markdown, "AuditQueryRequest")
	assert.Contains(t, markdown, "AuditEnvelope")
	assert.Contains(t, markdown, "NotificationBatch")
	assert.Contains(t, markdown, "email")
	assert.Contains(t, markdown, "phone")
	assert.Contains(t, markdown, "password")
	assert.Contains(t, markdown, "request_uuid")
	assert.Contains(t, markdown, "access_token")
	assert.Contains(t, markdown, "example-access-token")
	assert.Contains(t, markdown, "example-idempotency-key")
	assert.Contains(t, markdown, "login_count")
	assert.Contains(t, markdown, "display_name")
	assert.Contains(t, markdown, "fixed-nickname")
	assert.Contains(t, markdown, "Min value")
	assert.Contains(t, markdown, "Max value")
	assert.Contains(t, markdown, "Max length/size")
	assert.Contains(t, markdown, "**`email`**")
	assert.Contains(t, markdown, "**`USER_STATUS_ACTIVE`**")
	assert.Contains(t, markdown, "#### `CreateUserRequest` code example:")
	assert.Contains(t, markdown, "0.5")
	assert.Contains(t, markdown, "99.9")
	assert.Contains(t, markdown, "12")
	assert.Contains(t, markdown, "128")
	assert.Contains(t, markdown, "999999")
	assert.Contains(t, markdown, "USER_STATUS_ACTIVE")
	assert.Contains(t, markdown, "ROLE_ADMIN")
	assert.Contains(t, markdown, "Administrator role with full access.")
	assert.Contains(t, markdown, "PAYMENT_STATUS_CAPTURED")
	assert.Contains(t, markdown, "Payment has been captured successfully.")
	assert.Contains(t, markdown, "CURRENCY_USD")
	assert.Contains(t, markdown, "United States dollar.")
	assert.Contains(t, markdown, "REGION_EU_WEST")
	assert.Contains(t, markdown, "EU West region.")
	assert.Contains(t, markdown, "INVENTORY_STATE_AVAILABLE")
	assert.Contains(t, markdown, "AUDIT_ACTION_UPDATED")
	assert.Contains(t, markdown, "NOTIFICATION_CHANNEL_EMAIL")
	assert.Contains(t, markdown, "Email notification channel.")
	assert.Contains(t, markdown, "<session>")
	assert.Contains(t, markdown, "<refund>")
	assert.Contains(t, markdown, "pay_8f3c0d")
	assert.Contains(t, markdown, "sku-keyboard")
	assert.Contains(t, markdown, "trace_abcdef")
	assert.Contains(t, markdown, "tpl-welcome")
	assert.Contains(t, markdown, "Enums")
	assert.NotContains(t, markdown, "internal_note")
	assert.NotContains(t, markdown, "IgnoredMessage")
	assert.NotContains(t, markdown, "IgnoredEnum")
}

func TestCLIIntegrationGeneratesNestedPackages(t *testing.T) {
	root := t.TempDir()
	protoDir := filepath.Join(root, "proto")
	require.NoError(t, os.MkdirAll(filepath.Join(protoDir, "common"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(protoDir, "example", "v1"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(protoDir, "example", "v2"), 0o750))

	ufile.MustWrite(filepath.Join(protoDir, "common", "types.proto"), []byte(`syntax = "proto3";
package example.common;
// Shared value.
message Shared { string value = 1; }
`), 0o600)
	ufile.MustWrite(filepath.Join(protoDir, "example", "v1", "service.proto"), []byte(`syntax = "proto3";
package example.v1;
import "common/types.proto";
// V1 request.
message Request {
  enum State { STATE_UNSPECIFIED = 0; STATE_READY = 1; }
  example.common.Shared shared = 1;
  State state = 2;
}
`), 0o600)
	ufile.MustWrite(filepath.Join(protoDir, "example", "v1", "admin.proto"), []byte(`syntax = "proto3";
package example.v1;
// Administrative state.
enum AdminState { ADMIN_STATE_UNSPECIFIED = 0; ADMIN_STATE_READY = 1; }
`), 0o600)
	ufile.MustWrite(filepath.Join(protoDir, "example", "v2", "service.proto"), []byte(`syntax = "proto3";
package example.v2;
// V2 request.
message Request { string value = 1; }
`), 0o600)

	output := filepath.Join(root, "docs")
	run := runCLI(t,
		"-d", protoDir,
		"-o", output,
		"-split-by-package",
	)
	require.NoError(t, run.err, run.output)

	index := string(ufile.MustRead(filepath.Join(output, "README.md")))
	assert.Contains(t, index, "example/common/README.md")
	assert.Contains(t, index, "example/v1/README.md")
	assert.Contains(t, index, "example/v2/README.md")

	v1 := string(ufile.MustRead(filepath.Join(output, "example", "v1", "README.md")))
	assert.Contains(t, v1, "# Package `example.v1`")
	assert.Contains(t, v1, "example/v1/admin.proto")
	assert.Contains(t, v1, "example/v1/service.proto")
	assert.Contains(t, v1, "example.v1.Request")
	assert.Contains(t, v1, `<a name="example.v1.Request.State"></a>`)
	assert.Contains(t, v1, "AdminState")
}

func TestCLIIntegrationReportsFieldLocation(t *testing.T) {
	root := t.TempDir()
	protoDir := copyFixture(t, root, "broken_field.proto")

	run := runCLI(t,
		"-d", protoDir,
		"-o", filepath.Join(root, "out.md"),
	)
	require.Error(t, run.err)
	assert.Contains(t, run.output, "broken_field.proto:9")
	assert.Contains(t, run.output, "field name")
	assert.Contains(t, run.output, "unknown custom type provided: not_real")
}

func TestCLIIntegrationReportsCodeMarkerLocation(t *testing.T) {
	root := t.TempDir()
	protoDir := copyFixture(t, root, "broken_code.proto")

	run := runCLI(t,
		"-d", protoDir,
		"-o", filepath.Join(root, "out.md"),
	)
	require.Error(t, run.err)
	assert.Contains(t, run.output, "broken_code.proto:8")
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

func copyFixture(t *testing.T, root, name string) string {
	t.Helper()
	protoDir := filepath.Join(root, "proto")
	require.NoError(t, ufile.EnsureDir(protoDir, 0o750))
	ufile.MustWrite(filepath.Join(protoDir, name), ufile.MustRead(filepath.Join("testdata", "integration", name)), 0o600)
	return protoDir
}
