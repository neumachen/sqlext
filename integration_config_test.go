//go:build !integration

package sqlext

import (
	"os"
	"testing"
)

// Without the integration build tag no test opens a database, even when the
// integration variables are set. Fail rather than pass quietly, so a run meant
// to be an integration run cannot report success without one.
func TestIntegrationConfigurationNeedsBuildTag(t *testing.T) {
	for _, name := range []string{"SQLEXT_TEST_POSTGRES_DSN", "SQLEXT_TEST_POSTGRES_CLUSTER"} {
		if os.Getenv(name) != "" {
			t.Errorf("%s is set, but the integration tests are not built: run go test -tags integration, or unset it", name)
		}
	}
}
