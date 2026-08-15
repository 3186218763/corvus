package config

import (
	"os"
	"testing"

	"corvus/internal/testenv"
)

func TestMain(m *testing.M) {
	if os.Getenv("CORVUS_CONFIG_LOCK_HELPER") == "1" {
		os.Exit(m.Run())
	}
	testenv.RunWithIsolatedUserState(m)
}

// Guards the isolation above: a clean CI runner has an empty keyring and would
// stay green either way, so the substitution itself is what gets asserted.
