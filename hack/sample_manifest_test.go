package hack_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOverlappingRangeIPReservationSampleUsesSchemaFieldNames(t *testing.T) {
	sample, err := os.ReadFile(filepath.Join("..", "config", "samples", "whereabouts_v1alpha1_overlappingrangeipreservation.yaml"))
	if err != nil {
		t.Fatalf("reading ORIP sample: %v", err)
	}

	text := string(sample)
	for _, want := range []string{"podref:", "containerid:", "ifname:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("ORIP sample missing schema field %q", want)
		}
	}
	for _, disallowed := range []string{"podRef:", "containerID:", "ifName:"} {
		if strings.Contains(text, disallowed) {
			t.Fatalf("ORIP sample still contains non-schema field %q", disallowed)
		}
	}
}
