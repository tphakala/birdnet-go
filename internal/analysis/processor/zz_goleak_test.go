package processor

import (
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// Keep ignores narrowly scoped to known benign goroutines
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
	)
}
