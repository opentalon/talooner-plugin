// Package ruleset loads a tenant ruleset together with Talooner's strict base.
//
// Two rulesets are always compiled together: the tenant's, and the strict base
// the tenant imports. It has to be `import`, not string concatenation —
// `overrides` across two separately compiled programs is a compile error, so
// base and tenant must be one program (OPEN-QUESTIONS.md A5, engine.md). The
// loader writes the base to a temp dir and compiles the tenant source with that
// dir as the import base path, so the tenant's `import "talooner.tln"` resolves
// to the shipped base.
package ruleset

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opentalon/tln-language/pkg/tln"
)

//go:embed base/talooner.tln
var strictBase string

const (
	// BaseFileName is the filename the strict base is written under at compile
	// time, and so the name a tenant ruleset imports it by.
	BaseFileName = "talooner.tln"

	// BaseImport is the exact import statement a tenant ruleset must include to
	// load the strict base. Kept as one source of truth for docs and for the
	// verb/import enforcement in validate_ruleset (P-B4).
	BaseImport = `import "` + BaseFileName + `"`

	// TenantFile is the stable label the tenant ruleset compiles under. It is
	// what diagnostics report as their file, so positions are meaningful to the
	// tenant and the compile temp directory never leaks into a response.
	TenantFile = "ruleset.tln"
)

// Severity classifies a diagnostic.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Diagnostic is one compiler finding with its source position.
type Diagnostic struct {
	Severity Severity
	Message  string
	File     string
	Line     int
	Col      int
	Hint     string
}

// Compiled is a successfully loaded ruleset.
type Compiled struct {
	// Hash identifies the exact (strict base, tenant source) pair that
	// compiled, so a persisted decision records which ruleset produced it. It
	// covers the base too: bumping the base changes every tenant's hash.
	Hash string

	// Diagnostics are the non-error findings (warnings, info) surfaced on an
	// otherwise successful compile.
	Diagnostics []Diagnostic
}

// StrictBase returns the strict base ruleset source. Exposed so P-B3's
// .tln.test and tooling can read the exact bytes that ship.
func StrictBase() string { return strictBase }

// Load parses, validates and compiles a tenant ruleset with the strict base
// imported.
//
// On success it returns a *Compiled (with any non-error diagnostics) and a nil
// error. On a compile failure it returns the diagnostics — each carrying a
// line/column position — and a non-nil error. The tenant source is compiled
// verbatim, so diagnostic positions line up with what the tenant wrote.
func Load(tenantSource string) (*Compiled, []Diagnostic, error) {
	tenantPath, cleanup, err := baseDir()
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()

	if cerr := tln.Check(tenantSource, tln.WithFilename(tenantPath)); cerr != nil {
		return nil, relabel(diagnosticsFrom(cerr), tenantPath), cerr
	}

	diags := diagnosticsFrom(nil) // none on success today; kept for symmetry
	return &Compiled{Hash: hashRuleset(strictBase, tenantSource), Diagnostics: diags}, diags, nil
}

// Evaluate compiles a tenant ruleset with the strict base imported and runs it
// against store, returning the engine result — the fired actions with their
// arguments already resolved per matched row. A compile failure returns a
// *tln.CompileError; a runtime failure returns a plain error.
//
// The tenant ruleset is expected to import the strict base (BaseImport); loading
// resolves that import to the shipped base so base and tenant run as one program
// and defeasible resolution spans both.
func Evaluate(ctx context.Context, tenantSource string, store tln.FactStore) (*tln.Result, error) {
	tenantPath, cleanup, err := baseDir()
	if err != nil {
		return nil, err
	}
	defer cleanup()

	return tln.Run(ctx, tenantSource, tln.WithFilename(tenantPath), tln.WithFactStore(store))
}

// baseDir creates a temp directory holding the strict base under BaseFileName
// and returns the virtual tenant file path to compile/run against (it need not
// exist on disk — tln reads the source string and only resolves imports
// relative to this path's directory), plus a cleanup func.
func baseDir() (tenantPath string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "talooner-ruleset-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("ruleset: temp dir: %w", err)
	}
	if werr := os.WriteFile(filepath.Join(dir, BaseFileName), []byte(strictBase), 0o600); werr != nil {
		_ = os.RemoveAll(dir)
		return "", func() {}, fmt.Errorf("ruleset: write base: %w", werr)
	}
	return filepath.Join(dir, TenantFile), func() { _ = os.RemoveAll(dir) }, nil
}

// relabel rewrites the tenant temp path in diagnostics to the stable
// TenantFile label, so a compile temp directory never reaches a response.
// Imported-file diagnostics already carry the base's basename and are left as
// they are.
func relabel(diags []Diagnostic, tenantPath string) []Diagnostic {
	for i := range diags {
		if diags[i].File == tenantPath {
			diags[i].File = TenantFile
		}
	}
	return diags
}

// Validate reports whether a tenant ruleset is valid and returns every
// diagnostic — verb-vocabulary violations plus compile errors, each with a
// source position. A ruleset is valid only if it compiles (with the strict base
// imported) and every `do` verb is in AllowedVerbs. This backs the
// validate_ruleset action and `talooner rules validate`.
func Validate(tenantSource string) (valid bool, diags []Diagnostic) {
	diags = append(diags, CheckVerbs(tenantSource)...)

	// Load surfaces parse/compile/import errors (already relabelled to
	// TenantFile). The compiled result is discarded — validation only cares
	// about the diagnostics.
	if _, compileDiags, err := Load(tenantSource); err != nil {
		diags = append(diags, compileDiags...)
	}

	valid = true
	for _, d := range diags {
		if d.Severity == SeverityError {
			valid = false
			break
		}
	}
	return valid, diags
}

func hashRuleset(base, tenant string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(base))
	_, _ = h.Write([]byte{0}) // domain separator so (a,b) != (ab,"")
	_, _ = h.Write([]byte(tenant))
	return hex.EncodeToString(h.Sum(nil))
}

// diagnosticsFrom maps a tln compile error to positioned Diagnostics. A nil
// error yields no diagnostics; a non-CompileError is reported as a single
// positionless error so nothing is swallowed.
func diagnosticsFrom(err error) []Diagnostic {
	if err == nil {
		return nil
	}
	var ce *tln.CompileError
	if !errors.As(err, &ce) {
		return []Diagnostic{{Severity: SeverityError, Message: err.Error()}}
	}
	out := make([]Diagnostic, 0, len(ce.Diags))
	for _, d := range ce.Diags {
		out = append(out, Diagnostic{
			Severity: severityString(d.Severity),
			Message:  d.Message,
			File:     d.File,
			Line:     d.Line,
			Col:      d.Col,
			Hint:     d.Hint,
		})
	}
	return out
}

func severityString(s tln.Severity) Severity {
	switch s {
	case tln.SeverityWarning:
		return SeverityWarning
	case tln.SeverityInfo:
		return SeverityInfo
	default:
		return SeverityError
	}
}
