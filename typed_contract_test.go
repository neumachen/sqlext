package sqlext_test

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The package in testdata/contract must not compile: every declaration in it
// uses a type that the ColumnType constraint rejects. testdata is never
// matched by ./..., so only this test builds it. go test puts its own
// GOROOT/bin first on PATH, so "go" is the toolchain running the test.
func TestTypedCompileContract(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-gcflags=-e", "./testdata/contract")
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off")

	output, err := cmd.CombinedOutput()

	require.NoError(t, ctx.Err(), string(output))
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, string(output))
	require.Equal(t, 1, exit.ExitCode(), string(output))
	for _, rejected := range []string{"int", "float32", "*int64", "map[string]string", "struct{}", "CustomID", "any"} {
		require.Contains(t, string(output), rejected+" does not satisfy", string(output))
	}
}
