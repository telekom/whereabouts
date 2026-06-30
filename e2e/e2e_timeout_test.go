package whereabouts_e2e

import (
	"testing"
	"time"
)

func TestE2ETimeoutsCoverHostedRunnerLatency(t *testing.T) {
	if podDeleteTimeout < 2*time.Minute {
		t.Fatalf("podDeleteTimeout = %s, want at least 2m", podDeleteTimeout)
	}
	if allocationRecreateTimeout < 30*time.Second {
		t.Fatalf("allocationRecreateTimeout = %s, want at least 30s", allocationRecreateTimeout)
	}
	if allocationRecreateInterval > time.Second {
		t.Fatalf("allocationRecreateInterval = %s, want at most 1s", allocationRecreateInterval)
	}
}
