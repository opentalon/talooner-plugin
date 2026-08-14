package engine_test

import (
	"testing"

	"github.com/opentalon/talooner-plugin/internal/engine"
)

func TestValidateSource_Valid(t *testing.T) {
	src := `
workflow "ok" {
  step "one" {
    mcp "svc" "do" { arg "x" }
  }
}`
	if err := engine.ValidateSource(src); err != nil {
		t.Fatalf("valid ruleset should compile, got: %v", err)
	}
}

func TestValidateSource_ParseError(t *testing.T) {
	if err := engine.ValidateSource(`workflow "broken" {`); err == nil {
		t.Fatal("malformed ruleset should not compile")
	}
}
