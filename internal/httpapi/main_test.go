package httpapi

import (
	"testing"

	"go.uber.org/goleak"
)

// Gate 1: no goroutine may outlive the request that started it (proposal.md §6.3).
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
