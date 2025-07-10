package testing

import (
	"testing"
)

// TestThatWillFail is a temporary test that always fails to trigger the automated test engineer
func TestThatWillFail(t *testing.T) {
	t.Parallel()
	
	// This test is intentionally designed to fail to test our automated test engineer workflow
	expected := "success"
	actual := "failure"
	
	if actual != expected {
		t.Errorf("Expected %s, but got %s", expected, actual)
	}
}

// TestThatPasses is a test that should pass to ensure we don't break all tests
func TestThatPasses(t *testing.T) {
	t.Parallel()
	
	expected := "pass"
	actual := "pass"
	
	if actual != expected {
		t.Errorf("Expected %s, but got %s", expected, actual)
	}
}