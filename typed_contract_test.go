package dbsql_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTypedCompileContract(t *testing.T) {
	source, err := os.ReadFile("testdata/column_types.invalid.txt")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "contract.go")
	require.NoError(t, os.WriteFile(path, source, 0o600))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "test", "-mod=readonly", "-gcflags=-e", path)
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
