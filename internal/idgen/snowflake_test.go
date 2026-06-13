package idgen

import (
	"testing"
	"time"

	sqids "github.com/sqids/sqids-go"
)

func newTestSqids(t *testing.T) *sqids.Sqids {
	t.Helper()
	sq, err := sqids.New()
	if err != nil {
		t.Fatalf("sqids.New() error = %v", err)
	}
	return sq
}

func TestSnowflake_New_InvalidWorkerID(t *testing.T) {
	_, err := NewSnowflakeGenerator(1024, newTestSqids(t))
	if err == nil {
		t.Fatal("expected error for workerID 1024, got nil")
	}
}

func TestSnowflake_Generate_ReturnsCode(t *testing.T) {
	gen, err := NewSnowflakeGenerator(0, newTestSqids(t))
	if err != nil {
		t.Fatalf("NewSnowflakeGenerator(0) error = %v", err)
	}

	code, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if code == "" {
		t.Fatal("Generate() returned empty string")
	}
}

func TestSnowflake_Generate_UniqueCodesWithinSameMs(t *testing.T) {
	gen, err := NewSnowflakeGenerator(0, newTestSqids(t))
	if err != nil {
		t.Fatalf("NewSnowflakeGenerator(0) error = %v", err)
	}

	a, _ := gen.Generate()
	b, _ := gen.Generate()

	if a == b {
		t.Fatalf("Generate() returned duplicate code %q", a)
	}
}

func TestSnowflake_Generate_ClockRegressed(t *testing.T) {
	gen, err := NewSnowflakeGenerator(0, newTestSqids(t))
	if err != nil {
		t.Fatalf("NewSnowflakeGenerator(0) error = %v", err)
	}

	// simulate a clock 60 seconds in the future so the next call appears to go backward
	gen.lastMs = time.Now().UnixMilli() - epoch + 60_000

	_, err = gen.Generate()
	if err != ErrClockRegressed {
		t.Fatalf("Generate() error = %v, want ErrClockRegressed", err)
	}
}
