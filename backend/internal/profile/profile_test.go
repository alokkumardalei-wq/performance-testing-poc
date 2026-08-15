package profile

import (
	"testing"

	"k8s.io/utils/ptr"
)

func TestResolveDefaults(t *testing.T) {
	spec, err := Resolve("read_heavy", Overrides{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.ReadPercent != 95 || spec.WritePercent != 5 {
		t.Errorf("read_heavy mix = %d/%d", spec.ReadPercent, spec.WritePercent)
	}
	if spec.Profile != "read_heavy" {
		t.Errorf("profile name not carried: %q", spec.Profile)
	}
}

func TestResolveOverrides(t *testing.T) {
	spec, err := Resolve("smoke", Overrides{
		Threads:         ptr.To(32),
		DurationSeconds: ptr.To(45),
		SkipPrepare:     ptr.To(true),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.Threads != 32 || spec.DurationSeconds != 45 || !spec.SkipPrepare {
		t.Errorf("overrides not applied: %+v", spec)
	}
	// Untouched fields keep profile defaults.
	if spec.ReadPercent != 70 {
		t.Errorf("ReadPercent = %d, want 70", spec.ReadPercent)
	}
}

func TestResolveValidation(t *testing.T) {
	cases := []struct {
		name string
		o    Overrides
	}{
		{"zero threads", Overrides{Threads: ptr.To(0)}},
		{"too many threads", Overrides{Threads: ptr.To(5000)}},
		{"too short", Overrides{DurationSeconds: ptr.To(1)}},
		{"bad mix", Overrides{ReadPercent: ptr.To(80), WritePercent: ptr.To(80)}},
	}
	for _, c := range cases {
		if _, err := Resolve("smoke", c.o); err == nil {
			t.Errorf("%s: expected validation error", c.name)
		}
	}
	if _, err := Resolve("nope", Overrides{}); err == nil {
		t.Error("unknown profile: expected error")
	}
}

func TestListStable(t *testing.T) {
	if len(List()) < 5 {
		t.Errorf("expected at least 5 profiles, got %d", len(List()))
	}
}
