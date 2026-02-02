package usesmock

import (
	"testing"

	// This test file imports testmock - a test-only dependency: 
	// the production code in usesmock.go notably does not import
	// testmock
	"gta.test/testmock"
)

func TestService(t *testing.T) {
	mock := &testmock.MockService{}
	if got := mock.DoSomething(); got != "mocked" {
		t.Errorf("got %q, want %q", got, "mocked")
	}
}
