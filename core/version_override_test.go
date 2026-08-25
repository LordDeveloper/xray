package core

import "testing"

func TestApplyVersionOverride(t *testing.T) {
	ox, oy, oz := Version_x, Version_y, Version_z
	t.Cleanup(func() {
		Version_x, Version_y, Version_z = ox, oy, oz
	})

	applyVersionOverride("v1.0.7")
	if Version() != "1.0.7" {
		t.Fatalf("got %s want 1.0.7", Version())
	}

	applyVersionOverride("2.3.4")
	if Version() != "2.3.4" {
		t.Fatalf("got %s want 2.3.4", Version())
	}

	// Invalid overrides must be ignored.
	applyVersionOverride("bad")
	if Version() != "2.3.4" {
		t.Fatalf("invalid override changed version to %s", Version())
	}
}
