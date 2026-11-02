package pagewatch

import (
	"os"
	"testing"
	"time"
)

// TestMain pins time.Local to UTC before any test runs: the goldens hold
// text formatted in local time (e.g. a hold's "move undone on …" detail), so
// they must not depend on the host's time zone
func TestMain(m *testing.M) {
	time.Local = time.UTC
	os.Exit(m.Run())
}
