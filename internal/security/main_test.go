package security

import (
	"os"
	"testing"

	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestMain(m *testing.M) {
	conf.NewTestSettings().Apply()
	os.Exit(m.Run())
}
