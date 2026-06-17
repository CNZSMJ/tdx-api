package tdx

import "testing"

func TestStartupCodeRefreshUsesCache(t *testing.T) {
	t.Setenv(StartupCodeRefreshEnv, " cache ")
	if !startupCodeRefreshUsesCache() {
		t.Fatal("expected startup code refresh to use cache")
	}
}
