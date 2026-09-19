package zmx_test

import (
	"os"
	"testing"

	"github.com/husniadil/olympus/internal/testhome"
)

// TestMain gives the package's live tests a HOME of their own (§2.9).
func TestMain(m *testing.M) {
	os.Exit(testhome.Run(m))
}
