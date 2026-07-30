package zmachine

import "testing"

// TestVersion pins the reported version. Bumping the constant should be a
// deliberate change, so this test is expected to be updated along with it.
func TestVersion(t *testing.T) {
	const want = "0.1.6"

	if got := Version(); got != want {
		t.Errorf("Version() = %q, want %q", got, want)
	}
}
