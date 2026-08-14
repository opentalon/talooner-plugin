package auth_test

import (
	"errors"
	"testing"

	"github.com/opentalon/talooner-plugin/internal/auth"
)

func mustRegistry(t *testing.T, tenants map[string]auth.Tenant) *auth.Registry {
	t.Helper()
	r, err := auth.NewRegistry(tenants)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return r
}

func TestAuthenticate(t *testing.T) {
	r := mustRegistry(t, map[string]auth.Tenant{
		"k1": {Name: "acme", Features: []string{"llm_review"}},
	})

	t.Run("missing", func(t *testing.T) {
		if _, err := r.Authenticate(""); !errors.Is(err, auth.ErrMissingKey) {
			t.Fatalf("err = %v, want ErrMissingKey", err)
		}
	})
	t.Run("bad", func(t *testing.T) {
		if _, err := r.Authenticate("nope"); !errors.Is(err, auth.ErrBadKey) {
			t.Fatalf("err = %v, want ErrBadKey", err)
		}
	})
	t.Run("valid", func(t *testing.T) {
		got, err := r.Authenticate("k1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Name != "acme" || !got.HasFeature("llm_review") {
			t.Fatalf("tenant = %+v, want acme with llm_review", got)
		}
	})
}

func TestEmptyRegistryFailsClosed(t *testing.T) {
	r := mustRegistry(t, nil)
	if _, err := r.Authenticate("anything"); !errors.Is(err, auth.ErrBadKey) {
		t.Fatalf("empty registry err = %v, want ErrBadKey", err)
	}
}

func TestNewRegistryRejectsEmptyKey(t *testing.T) {
	if _, err := auth.NewRegistry(map[string]auth.Tenant{"": {Name: "x"}}); err == nil {
		t.Fatal("empty api key should be rejected")
	}
}
