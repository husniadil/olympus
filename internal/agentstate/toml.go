package agentstate

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// A subset of TOML, sufficient for the manifests and nothing more.
//
// The manifests use comments, bare keys, basic and literal strings, integers,
// booleans, arrays (spanning lines, holding strings or inline tables), inline
// tables, and `[[rules]]` array-of-tables headers. Everything outside that
// subset is an error rather than a guess: a manifest is loaded by a test, so
// an upstream change that reaches for more TOML fails the gate instead of
// silently parsing to something else. A dependency for this would spend one
// of the three the budget allows on a hundred lines of scanning.

// A tomlTable is a decoded table; values are string, int64, bool,
// []any or tomlTable.
type tomlTable map[string]any

type tomlParser struct {
	src  string
	pos  int
	line int
}

// parseTOML decodes a document into its top-level table. `[[name]]` headers
// append a table to the array under name; dotted keys and `[name]` headers
// are outside the subset.
func parseTOML(src string) (tomlTable, error) {
	p := &tomlParser{src: src, line: 1}
	root := tomlTable{}
	current := root
	for {
		p.skipBlank()
		if p.eof() {
			return root, nil
		}
		if p.peek() == '[' {
			if !strings.HasPrefix(p.src[p.pos:], "[[") {
				return nil, p.errorf("only [[array-of-tables]] headers are supported")
			}
			p.pos += 2
			name, err := p.key()
			if err != nil {
				return nil, err
			}
			p.skipSpaces()
			if !strings.HasPrefix(p.src[p.pos:], "]]") {
				return nil, p.errorf("expected ]] after table name %q", name)
			}
			p.pos += 2
			if err := p.endOfLine(); err != nil {
				return nil, err
			}
			table := tomlTable{}
			existing, _ := root[name].([]any)
			if _, present := root[name]; present && existing == nil {
				return nil, p.errorf("%q is already a value, not an array of tables", name)
			}
			root[name] = append(existing, table)
			current = table
			continue
		}
		key, err := p.key()
		if err != nil {
			return nil, err
		}
		p.skipSpaces()
		if p.eof() || p.peek() != '=' {
			return nil, p.errorf("expected = after key %q", key)
		}
		p.pos++
		p.skipSpaces()
		value, err := p.value()
		if err != nil {
			return nil, err
		}
		if _, dup := current[key]; dup {
			return nil, p.errorf("key %q is defined twice", key)
		}
		current[key] = value
		if err := p.endOfLine(); err != nil {
			return nil, err
		}
	}
}

func (p *tomlParser) eof() bool  { return p.pos >= len(p.src) }
func (p *tomlParser) peek() byte { return p.src[p.pos] }

func (p *tomlParser) errorf(format string, args ...any) error {
	return fmt.Errorf("toml line %d: %s", p.line, fmt.Sprintf(format, args...))
}

// skipSpaces skips horizontal whitespace only.
func (p *tomlParser) skipSpaces() {
	for !p.eof() && (p.peek() == ' ' || p.peek() == '\t') {
		p.pos++
	}
}

// skipBlank skips whitespace, newlines and comments.
func (p *tomlParser) skipBlank() {
	for !p.eof() {
		switch p.peek() {
		case ' ', '\t', '\r':
			p.pos++
		case '\n':
			p.pos++
			p.line++
		case '#':
			for !p.eof() && p.peek() != '\n' {
				p.pos++
			}
		default:
			return
		}
	}
}

// endOfLine requires nothing but a comment before the next newline.
func (p *tomlParser) endOfLine() error {
	p.skipSpaces()
	if p.eof() {
		return nil
	}
	switch p.peek() {
	case '#':
		for !p.eof() && p.peek() != '\n' {
			p.pos++
		}
		return nil
	case '\r':
		p.pos++
		if p.eof() || p.peek() != '\n' {
			return p.errorf("stray carriage return")
		}
		fallthrough
	case '\n':
		p.pos++
		p.line++
		return nil
	}
	return p.errorf("unexpected %q after a value", p.peek())
}

// key reads a bare key: letters, digits, underscores and dashes.
func (p *tomlParser) key() (string, error) {
	p.skipSpaces()
	start := p.pos
	for !p.eof() {
		c := p.peek()
		if c == '_' || c == '-' || unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c)) {
			p.pos++
			continue
		}
		break
	}
	if start == p.pos {
		return "", p.errorf("expected a bare key")
	}
	return p.src[start:p.pos], nil
}

func (p *tomlParser) value() (any, error) {
	if p.eof() {
		return nil, p.errorf("expected a value")
	}
	switch c := p.peek(); {
	case c == '"':
		return p.basicString()
	case c == '\'':
		return p.literalString()
	case c == '[':
		return p.array()
	case c == '{':
		return p.inlineTable()
	case c == 't' || c == 'f':
		for _, word := range []string{"true", "false"} {
			if strings.HasPrefix(p.src[p.pos:], word) {
				p.pos += len(word)
				return word == "true", nil
			}
		}
		return nil, p.errorf("expected true or false")
	case c == '-' || c == '+' || unicode.IsDigit(rune(c)):
		start := p.pos
		p.pos++
		for !p.eof() && (unicode.IsDigit(rune(p.peek())) || p.peek() == '_') {
			p.pos++
		}
		n, err := strconv.ParseInt(strings.ReplaceAll(p.src[start:p.pos], "_", ""), 10, 64)
		if err != nil {
			return nil, p.errorf("bad integer %q", p.src[start:p.pos])
		}
		return n, nil
	}
	return nil, p.errorf("unsupported value starting with %q", p.peek())
}

// basicString reads a "double-quoted" string with the escapes the spec
// defines; multi-line strings are outside the subset.
func (p *tomlParser) basicString() (string, error) {
	if strings.HasPrefix(p.src[p.pos:], `"""`) {
		return "", p.errorf("multi-line strings are not supported")
	}
	p.pos++
	var out strings.Builder
	for {
		if p.eof() || p.peek() == '\n' {
			return "", p.errorf("unterminated string")
		}
		c := p.peek()
		p.pos++
		switch c {
		case '"':
			return out.String(), nil
		case '\\':
			if p.eof() {
				return "", p.errorf("unterminated escape")
			}
			e := p.peek()
			p.pos++
			switch e {
			case 'b':
				out.WriteByte('\b')
			case 't':
				out.WriteByte('\t')
			case 'n':
				out.WriteByte('\n')
			case 'f':
				out.WriteByte('\f')
			case 'r':
				out.WriteByte('\r')
			case '"':
				out.WriteByte('"')
			case '\\':
				out.WriteByte('\\')
			case 'u', 'U':
				width := 4
				if e == 'U' {
					width = 8
				}
				if p.pos+width > len(p.src) {
					return "", p.errorf("truncated unicode escape")
				}
				code, err := strconv.ParseUint(p.src[p.pos:p.pos+width], 16, 32)
				if err != nil {
					return "", p.errorf("bad unicode escape")
				}
				p.pos += width
				out.WriteRune(rune(code))
			default:
				return "", p.errorf("unknown escape \\%c", e)
			}
		default:
			out.WriteByte(c)
		}
	}
}

// literalString reads a 'single-quoted' string, which has no escapes at all:
// what is between the quotes is the value, which is why the manifests spell
// their regexes this way.
func (p *tomlParser) literalString() (string, error) {
	if strings.HasPrefix(p.src[p.pos:], "'''") {
		return "", p.errorf("multi-line strings are not supported")
	}
	p.pos++
	end := strings.IndexAny(p.src[p.pos:], "'\n")
	if end < 0 || p.src[p.pos+end] != '\'' {
		return "", p.errorf("unterminated literal string")
	}
	value := p.src[p.pos : p.pos+end]
	p.pos += end + 1
	return value, nil
}

func (p *tomlParser) array() ([]any, error) {
	p.pos++
	out := []any{}
	for {
		p.skipBlank()
		if p.eof() {
			return nil, p.errorf("unterminated array")
		}
		if p.peek() == ']' {
			p.pos++
			return out, nil
		}
		value, err := p.value()
		if err != nil {
			return nil, err
		}
		out = append(out, value)
		p.skipBlank()
		if p.eof() {
			return nil, p.errorf("unterminated array")
		}
		switch p.peek() {
		case ',':
			p.pos++
		case ']':
		default:
			return nil, p.errorf("expected , or ] in array")
		}
	}
}

func (p *tomlParser) inlineTable() (tomlTable, error) {
	p.pos++
	out := tomlTable{}
	for {
		p.skipBlank()
		if p.eof() {
			return nil, p.errorf("unterminated inline table")
		}
		if p.peek() == '}' {
			p.pos++
			return out, nil
		}
		key, err := p.key()
		if err != nil {
			return nil, err
		}
		p.skipSpaces()
		if p.eof() || p.peek() != '=' {
			return nil, p.errorf("expected = after key %q", key)
		}
		p.pos++
		p.skipSpaces()
		value, err := p.value()
		if err != nil {
			return nil, err
		}
		if _, dup := out[key]; dup {
			return nil, p.errorf("key %q is defined twice", key)
		}
		out[key] = value
		p.skipBlank()
		if p.eof() {
			return nil, p.errorf("unterminated inline table")
		}
		switch p.peek() {
		case ',':
			p.pos++
		case '}':
		default:
			return nil, p.errorf("expected , or } in inline table")
		}
	}
}
