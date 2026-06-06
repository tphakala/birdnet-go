package security

import (
	"os"
	"testing"

	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

func TestMain(m *testing.M) {
	conftest.NewTestSettings().Apply()
	os.Exit(m.Run())
}
