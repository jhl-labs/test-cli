// Package ingest converts native test and coverage artifacts (JUnit XML, go
// test -json, Cobertura, LCOV, JaCoCo, Go coverage profiles) into the
// normalized model. Format detection is content-based ("sniffing") so callers
// do not need to know which tool produced a file.
package ingest

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/jhl-labs/test-cli/internal/model"
)

// TestFormat and CoverageFormat enumerate the artifact kinds we recognize.
const (
	FormatUnknown   = "unknown"
	FormatJUnit     = "junit"
	FormatGoJSON    = "go-json"
	FormatCobertura = "cobertura"
	FormatLCOV      = "lcov"
	FormatJaCoCo    = "jacoco"
	FormatGoCover   = "go-cover"
)

// DetectTestFormat sniffs a test-result artifact's format from its content.
func DetectTestFormat(data []byte) string {
	switch {
	case looksLikeGoJSON(data):
		return FormatGoJSON
	case looksLikeJUnit(data):
		return FormatJUnit
	default:
		return FormatUnknown
	}
}

// DetectCoverageFormat sniffs a coverage artifact's format from its content.
func DetectCoverageFormat(data []byte) string {
	switch {
	case looksLikeGoCover(data):
		return FormatGoCover
	case looksLikeJaCoCo(data):
		return FormatJaCoCo
	case looksLikeCobertura(data):
		return FormatCobertura
	case looksLikeLCOV(data):
		return FormatLCOV
	default:
		return FormatUnknown
	}
}

// LoadTests reads a test artifact file and returns normalized suites. The
// language hint is attached to suites where the format does not carry one.
func LoadTests(path, language string) ([]model.TestSuite, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, FormatUnknown, err
	}
	format := DetectTestFormat(data)
	switch format {
	case FormatGoJSON:
		suites, err := ParseGoJSON(data)
		return suites, format, err
	case FormatJUnit:
		suites, err := ParseJUnit(data, language)
		return suites, format, err
	default:
		return nil, FormatUnknown, fmt.Errorf("unrecognized test report format: %s", path)
	}
}

// LoadCoverage reads a coverage artifact file and returns normalized per-file
// coverage.
func LoadCoverage(path, language string) ([]model.FileCoverage, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, FormatUnknown, err
	}
	format := DetectCoverageFormat(data)
	switch format {
	case FormatGoCover:
		files, err := ParseGoCover(data, language)
		return files, format, err
	case FormatJaCoCo:
		files, err := ParseJaCoCo(data, language)
		return files, format, err
	case FormatCobertura:
		files, err := ParseCobertura(data, language)
		return files, format, err
	case FormatLCOV:
		files, err := ParseLCOV(data, language)
		return files, format, err
	default:
		return nil, FormatUnknown, fmt.Errorf("unrecognized coverage format: %s", path)
	}
}

// --- shared helpers ---

func sniffHead(data []byte) string {
	const n = 4096
	if len(data) > n {
		data = data[:n]
	}
	return string(data)
}

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// normalizePath canonicalizes a file path to forward slashes and trims common
// prefixes so coverage paths line up with repository-relative source paths.
func normalizePath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "./")
	return p
}
