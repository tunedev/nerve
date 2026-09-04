package duckparquet_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/leak"
)

func TestMain(m *testing.M) { leak.VerifyTestMain(m) }
