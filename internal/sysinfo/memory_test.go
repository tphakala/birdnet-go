package sysinfo

import "testing"

func TestCurrentProcessRSS(t *testing.T) {
	t.Parallel()
	rss, err := CurrentProcessRSS()
	if err != nil {
		t.Fatalf("CurrentProcessRSS returned error: %v", err)
	}
	if rss == 0 {
		t.Fatal("expected non-zero RSS for the running test process")
	}
}
