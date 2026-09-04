package agentstate

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// A Manifest is one agent's screen-detection rules: how to tell, from what a
// pane shows, whether that agent is working, idle or blocked.
//
// The rule language is the one herdr's manifests are written in, ported so
// the vendored manifests evaluate the same way here. A rule is a region of
// the input — a slice of the screen, or the OSC title — and a gate over it;
// the highest-priority rule whose gate matches names the state, the first in
// file order among equals.
type Manifest struct {
	ID      string
	Aliases []string
	Rules   []Rule
}

// A Rule is one way an agent's state shows on screen.
type Rule struct {
	ID       string
	State    State
	Priority int
	Region   string
	// SkipStateUpdate marks a rule that recognises a transient overlay — a
	// transcript viewer, a model picker — through which the agent's real
	// state cannot be read. Its state is always unknown.
	SkipStateUpdate bool
	VisibleIdle     bool
	VisibleBlocker  bool
	VisibleWorking  bool
	gate            gate
}

// A gate is the conjunction of every matcher on it: all its substrings
// present, every regex matching the region, every line regex matching some
// line, every `all` gate matching, at least one `any` gate matching where
// there are any, and no `not` gate matching.
type gate struct {
	// contains is lowercased at compile time; the region is lowercased once
	// per rule, so a substring match is case-insensitive.
	contains  []string
	regex     []*regexp.Regexp
	lineRegex []*regexp.Regexp
	all       []gate
	any       []gate
	not       []gate
}

// Limits herdr enforces on a manifest, kept so a vendored manifest that
// outgrows them is noticed here too.
const (
	maxRulesPerManifest = 128
	maxGateDepth        = 8
	maxTotalGates       = 512
	maxMatchersPerGate  = 32
	maxTotalMatchers    = 1024
	maxMatcherChars     = 512
)

// ParseManifest decodes a manifest from its TOML source, validates it the way
// herdr does, and compiles its patterns. Any pattern Go's RE2 cannot compile
// is an error naming the rule and the pattern, so the load test that reads
// every vendored manifest reports exactly what needs rewriting.
func ParseManifest(src string) (*Manifest, error) {
	doc, err := parseTOML(src)
	if err != nil {
		return nil, err
	}
	m := &Manifest{}
	for key, value := range doc {
		switch key {
		case "id":
			m.ID, err = asString(value, key)
		case "aliases":
			m.Aliases, err = asStrings(value, key)
		case "version", "min_engine_version", "updated_at":
			// Upstream's update-channel metadata; nothing here reads them.
		case "rules":
			err = m.decodeRules(value)
		default:
			err = fmt.Errorf("unknown manifest field %q", key)
		}
		if err != nil {
			return nil, err
		}
	}
	if m.ID == "" {
		return nil, fmt.Errorf("manifest has no id")
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("manifest %s: %w", m.ID, err)
	}
	return m, nil
}

func (m *Manifest) decodeRules(value any) error {
	rows, ok := value.([]any)
	if !ok {
		return fmt.Errorf("rules must be an array of tables")
	}
	for i, row := range rows {
		table, ok := row.(tomlTable)
		if !ok {
			return fmt.Errorf("rules[%d] is not a table", i)
		}
		rule, err := decodeRule(table)
		if err != nil {
			return fmt.Errorf("rules[%d]: %w", i, err)
		}
		m.Rules = append(m.Rules, rule)
	}
	return nil
}

func decodeRule(table tomlTable) (Rule, error) {
	rule := Rule{Region: "whole_recent", State: Unknown}
	var err error
	for key, value := range table {
		switch key {
		case "id":
			rule.ID, err = asString(value, key)
		case "state":
			var s string
			s, err = asString(value, key)
			if err == nil {
				rule.State, err = parseState(s)
			}
		case "priority":
			n, ok := value.(int64)
			if !ok {
				err = fmt.Errorf("priority must be an integer")
			}
			rule.Priority = int(n)
		case "region":
			rule.Region, err = asString(value, key)
		case "visible_idle":
			rule.VisibleIdle, err = asBool(value, key)
		case "visible_blocker":
			rule.VisibleBlocker, err = asBool(value, key)
		case "visible_working":
			rule.VisibleWorking, err = asBool(value, key)
		case "skip_state_update":
			rule.SkipStateUpdate, err = asBool(value, key)
		case "all", "any", "not", "contains", "regex", "line_regex":
			// Decoded with the gate below.
		default:
			err = fmt.Errorf("unknown rule field %q", key)
		}
		if err != nil {
			return Rule{}, err
		}
	}
	rule.gate, err = decodeGate(table)
	if err != nil {
		return Rule{}, fmt.Errorf("rule %s: %w", rule.ID, err)
	}
	return rule, nil
}

// decodeGate reads the matcher fields off a table — a rule's own, or a
// nested gate's, where every other key is an error.
func decodeGate(table tomlTable) (gate, error) {
	var g gate
	var err error
	for key, value := range table {
		switch key {
		case "contains":
			var needles []string
			needles, err = asStrings(value, key)
			for _, needle := range needles {
				g.contains = append(g.contains, strings.ToLower(needle))
			}
		case "regex":
			g.regex, err = asPatterns(value, key)
		case "line_regex":
			g.lineRegex, err = asPatterns(value, key)
		case "all":
			g.all, err = asGates(value, key)
		case "any":
			g.any, err = asGates(value, key)
		case "not":
			g.not, err = asGates(value, key)
		}
		if err != nil {
			return gate{}, err
		}
	}
	return g, nil
}

func asString(value any, key string) (string, error) {
	s, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return s, nil
}

func asBool(value any, key string) (bool, error) {
	b, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return b, nil
}

func asStrings(value any, key string) ([]string, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s must be an array of strings", key)
		}
		out = append(out, s)
	}
	return out, nil
}

func asPatterns(value any, key string) ([]*regexp.Regexp, error) {
	sources, err := asStrings(value, key)
	if err != nil {
		return nil, err
	}
	out := make([]*regexp.Regexp, 0, len(sources))
	for _, source := range sources {
		re, err := regexp.Compile(source)
		if err != nil {
			return nil, fmt.Errorf("%s pattern %q does not compile: %w", key, source, err)
		}
		out = append(out, re)
	}
	return out, nil
}

func asGates(value any, key string) ([]gate, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array of tables", key)
	}
	out := make([]gate, 0, len(items))
	for i, item := range items {
		table, ok := item.(tomlTable)
		if !ok {
			return nil, fmt.Errorf("%s[%d] is not a table", key, i)
		}
		for field := range table {
			switch field {
			case "all", "any", "not", "contains", "regex", "line_regex":
			default:
				return nil, fmt.Errorf("%s[%d]: unknown gate field %q", key, i, field)
			}
		}
		g, err := decodeGate(table)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", key, i, err)
		}
		out = append(out, g)
	}
	return out, nil
}

// validate applies herdr's structural checks: at least one rule, bounded
// size, a skip rule neutral in state and evidence, a known region, and every
// gate carrying a positive matcher.
func (m *Manifest) validate() error {
	if len(m.Rules) == 0 {
		return fmt.Errorf("must contain at least one rule")
	}
	if len(m.Rules) > maxRulesPerManifest {
		return fmt.Errorf("contains %d rules, max is %d", len(m.Rules), maxRulesPerManifest)
	}
	var c complexity
	for _, rule := range m.Rules {
		if strings.TrimSpace(rule.ID) == "" {
			return fmt.Errorf("rule id must not be empty")
		}
		if rule.SkipStateUpdate {
			if rule.State != Unknown {
				return fmt.Errorf("rule %s uses skip_state_update without state = \"unknown\"", rule.ID)
			}
			if rule.VisibleIdle || rule.VisibleBlocker || rule.VisibleWorking {
				return fmt.Errorf("rule %s uses skip_state_update with visible state evidence", rule.ID)
			}
		}
		if !validRegion(rule.Region) {
			return fmt.Errorf("rule %s uses invalid region: %s", rule.ID, strings.TrimSpace(rule.Region))
		}
		if err := validateGate(rule.gate, "rule", 0, &c); err != nil {
			return fmt.Errorf("rule %s has invalid matcher gates: %w", rule.ID, err)
		}
	}
	return nil
}

type complexity struct{ gates, matchers int }

func validateGate(g gate, context string, depth int, c *complexity) error {
	if depth > maxGateDepth {
		return fmt.Errorf("%s exceeds max gate depth %d", context, maxGateDepth)
	}
	c.gates++
	if c.gates > maxTotalGates {
		return fmt.Errorf("manifest exceeds max gate count %d", maxTotalGates)
	}
	if err := validateMatcherLimits(g, context, c); err != nil {
		return err
	}
	if !g.hasPositiveMatcher() {
		return fmt.Errorf("%s must contain a positive matcher", context)
	}
	for _, nested := range g.all {
		if err := validateGate(nested, "all gate", depth+1, c); err != nil {
			return err
		}
	}
	for _, nested := range g.any {
		if err := validateGate(nested, "any gate", depth+1, c); err != nil {
			return err
		}
	}
	for _, nested := range g.not {
		if !nested.hasAnyMatcher() {
			return fmt.Errorf("%s contains an empty not gate", context)
		}
		if err := validateNotGate(nested, depth+1, c); err != nil {
			return err
		}
	}
	return nil
}

// validateNotGate is the one place a gate may be purely negative: a `not`
// holding only another `not`.
func validateNotGate(g gate, depth int, c *complexity) error {
	if depth > maxGateDepth {
		return fmt.Errorf("not gate exceeds max gate depth %d", maxGateDepth)
	}
	c.gates++
	if c.gates > maxTotalGates {
		return fmt.Errorf("manifest exceeds max gate count %d", maxTotalGates)
	}
	if err := validateMatcherLimits(g, "not gate", c); err != nil {
		return err
	}
	if !g.hasAnyMatcher() {
		return fmt.Errorf("not gate must contain a matcher")
	}
	for _, nested := range g.all {
		if err := validateGate(nested, "not all gate", depth+1, c); err != nil {
			return err
		}
	}
	for _, nested := range g.any {
		if err := validateGate(nested, "not any gate", depth+1, c); err != nil {
			return err
		}
	}
	for _, nested := range g.not {
		if err := validateNotGate(nested, depth+1, c); err != nil {
			return err
		}
	}
	return nil
}

func validateMatcherLimits(g gate, context string, c *complexity) error {
	count := len(g.contains) + len(g.regex) + len(g.lineRegex)
	if count > maxMatchersPerGate {
		return fmt.Errorf("%s has %d direct matchers, max is %d", context, count, maxMatchersPerGate)
	}
	c.matchers += count
	if c.matchers > maxTotalMatchers {
		return fmt.Errorf("manifest exceeds max matcher count %d", maxTotalMatchers)
	}
	for _, needle := range g.contains {
		if len([]rune(needle)) > maxMatcherChars {
			return fmt.Errorf("%s matcher exceeds max length %d", context, maxMatcherChars)
		}
	}
	for _, patterns := range [][]*regexp.Regexp{g.regex, g.lineRegex} {
		for _, re := range patterns {
			if len([]rune(re.String())) > maxMatcherChars {
				return fmt.Errorf("%s matcher exceeds max length %d", context, maxMatcherChars)
			}
		}
	}
	return nil
}

func (g gate) hasPositiveMatcher() bool {
	return len(g.contains) > 0 || len(g.regex) > 0 || len(g.lineRegex) > 0 || len(g.all) > 0 || len(g.any) > 0
}

func (g gate) hasAnyMatcher() bool { return g.hasPositiveMatcher() || len(g.not) > 0 }

// matches evaluates the gate over a region. text is the region as captured;
// lower is the same text lowercased once by the caller, for the substring
// matchers.
func (g gate) matches(text, lower string) bool {
	for _, needle := range g.contains {
		if !strings.Contains(lower, needle) {
			return false
		}
	}
	for _, re := range g.regex {
		if !re.MatchString(text) {
			return false
		}
	}
	if len(g.lineRegex) > 0 {
		lines := splitLines(text)
		for _, re := range g.lineRegex {
			matched := false
			for _, line := range lines {
				if re.MatchString(line) {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
	}
	for _, nested := range g.all {
		if !nested.matches(text, lower) {
			return false
		}
	}
	if len(g.any) > 0 {
		matched := false
		for _, nested := range g.any {
			if nested.matches(text, lower) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, nested := range g.not {
		if nested.matches(text, lower) {
			return false
		}
	}
	return true
}

// Result is what evaluating a manifest against an input found.
type Result struct {
	// State is the state the winning rule names, or Unknown when no rule
	// matched. It is Unknown too when the winning rule is a
	// skip_state_update rule: those recognise a screen the state cannot be
	// read through.
	State State
	// Rule is the id of the rule that matched, empty when none did.
	Rule string
	// SkipStateUpdate is set when the winning rule was a skip rule.
	SkipStateUpdate bool
}

// Evaluate finds the rule the input satisfies.
//
// Rules are tried in file order and the highest priority wins; a later rule
// replaces an earlier match only when its priority is strictly greater, so
// among equals the first in the file stands. When nothing matches the answer
// is Unknown. This is the one deliberate departure from herdr, whose engine
// falls back to idle for a known agent: herdr owns the pane and watches it
// continuously, so a screen with no evidence there is a settled screen. Here
// a snapshot is read once, and a state the screen does not show is a guess
// the listing must not make.
func (m *Manifest) Evaluate(in Input) Result {
	var winner *Rule
	for i := range m.Rules {
		rule := &m.Rules[i]
		text := region(in, rule.Region)
		if !rule.gate.matches(text, strings.ToLower(text)) {
			continue
		}
		if winner == nil || rule.Priority > winner.Priority {
			winner = rule
		}
	}
	if winner == nil {
		return Result{State: Unknown}
	}
	out := Result{State: winner.State, Rule: winner.ID, SkipStateUpdate: winner.SkipStateUpdate}
	if winner.SkipStateUpdate {
		out.State = Unknown
	}
	return out
}

// RuleIDs lists the manifest's rules in priority order, highest first, ties
// in file order — the order Evaluate effectively applies.
func (m *Manifest) RuleIDs() []string {
	ordered := make([]Rule, len(m.Rules))
	copy(ordered, m.Rules)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Priority > ordered[j].Priority })
	ids := make([]string, len(ordered))
	for i, rule := range ordered {
		ids[i] = rule.ID
	}
	return ids
}
