package olympus_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every normative section of the behavior spec must be traceable to a test.
//
// This repo is spec-first: the specification leads and the code follows. That
// only holds if a rule and the test that enforces it can be found from each
// other — otherwise "the spec says MUST" and "something checks it" drift apart
// silently, which is the failure the spec-first rule exists to prevent.
//
// The check is deliberately shallow. It asserts that some test CITES each
// MUST-bearing section, not that the citation is honest — a test can name a
// section and check the wrong thing, and no mechanical check can tell. What it
// does catch is the case that actually happens: a rule written into the spec
// with nothing anywhere pointing back at it.
//
// It was written after an audit measured citations and reported three sections
// as uncovered. All three were in fact covered, by tests that simply did not
// name them. The false positives were the finding: traceability was the thing
// missing, not coverage.

func TestEveryNormativeSectionIsCitedByATest(t *testing.T) {
	t.Parallel()

	spec, err := os.ReadFile(filepath.Join("docs", "terminal-behavior.md"))
	if err != nil {
		t.Fatalf("reading the spec: %v", err)
	}

	cited := citationsInTests(t)
	var uncited []string
	for _, section := range mustBearingSections(string(spec)) {
		if !coveredBy(section, cited) {
			uncited = append(uncited, section)
		}
	}

	if len(uncited) > 0 {
		t.Errorf("these sections say MUST and no test names them:\n  §%s\n\n"+
			"Either the rule has no test — write one — or the test exists and does not cite the\n"+
			"section, which makes the rule untraceable. Add the §number to the test's comment.",
			strings.Join(uncited, "\n  §"))
	}
}

// The check has to be able to fail, or it is decoration. A section number that
// cannot exist must come back uncited.
func TestTheCitationCheckWouldNoticeAnUncitedRule(t *testing.T) {
	t.Parallel()

	cited := citationsInTests(t)
	if coveredBy("99.9", cited) {
		t.Error("a section that does not exist was reported as cited, so the check cannot fail")
	}
}

// mustBearingSections returns every §number in the spec whose body contains a
// MUST.
//
// The exemption is the MUST itself, applied per section: a section whose body
// states no requirement is reference material and is skipped by name, here,
// because it carries nothing to trace. There is no range cap. One used to stop
// the walk at the backend contract, which exempted the MCP door, the testing
// requirements and the reserved-identifier registry as a block — sections that
// do state MUSTs, and whose rules were therefore untraceable by construction
// rather than by any judgement about them.
func mustBearingSections(spec string) []string {
	heading := regexp.MustCompile(`(?m)^#{2,4} (\d+(?:\.\d+)*)\.? [^\n]*$`)
	locations := heading.FindAllStringSubmatchIndex(spec, -1)

	var out []string
	for i, loc := range locations {
		number := spec[loc[2]:loc[3]]
		end := len(spec)
		if i+1 < len(locations) {
			end = locations[i+1][0]
		}
		body := spec[loc[1]:end]

		if !strings.Contains(body, "MUST") {
			continue
		}
		// A rule the spec itself hands to a backend's own tests is not expected
		// in the shared vocabulary, but is still expected to be cited SOMEWHERE
		// — which is what this check asks for, so it stays in the list.
		out = append(out, number)
	}
	sort.Strings(out)
	return out
}

// citationsInTests collects every §number appearing in any _test.go file.
func citationsInTests(t *testing.T) map[string]bool {
	t.Helper()

	cited := map[string]bool{}
	reference := regexp.MustCompile(`§(\d+(?:\.\d+)*)`)

	// The repo root, from this package's directory.
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// The conformance suite's cases live in ORDINARY .go files under
		// backend/backendtest — it is a library other packages run, not a test
		// binary — so restricting this to _test.go misses the single richest
		// source of citations in the repo. Found by this check failing on two
		// sections that suite names in full.
		inSuite := strings.HasPrefix(filepath.ToSlash(path), "backend/backendtest/")
		if info.IsDir() || !(strings.HasSuffix(path, "_test.go") || (inSuite && strings.HasSuffix(path, ".go"))) {
			return nil
		}
		// This file's own prose mentions section numbers; counting them would
		// let it satisfy itself.
		if filepath.Base(path) == "spec_coverage_test.go" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range reference.FindAllStringSubmatch(string(content), -1) {
			cited[match[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tests: %v", err)
	}
	return cited
}

// coveredBy requires the section's OWN number, not a parent's.
//
// It began as parent-covers-child — a test naming §8 counting for §8.3 — and a
// mutation showed that made the check nearly worthless: a brand-new §14.9 with
// an untested MUST passed, because something already cited §14. Every new
// subsection under an already-cited section was free.
//
// Exact matching cost five citations to adopt, and every one of the five turned
// out to have a real test that simply did not name its rule. That is the ratio
// worth paying.
func coveredBy(section string, cited map[string]bool) bool {
	return cited[section]
}
