package httpx

import (
	"fmt"
	"unicode/utf16"
)

const jsonContextRadius = 10

// jsonScanError mirrors the diagnostics V8 produces for JSON.parse, because
// the legacy API returns that text verbatim to clients.
type jsonScanError struct {
	message string
}

func (e *jsonScanError) Error() string { return e.message }

type jsonScanner struct {
	source   []uint16
	pos      int
	valuePos int
	depth    int
	brackets bool
}

// scanJSON validates one JSON document and reports the first problem using
// V8's wording. A malformed top-level value is reported as an unexpected
// token at the start of that value, while nested values get the precise
// diagnostic.
func scanJSON(source string) *jsonScanError {
	scanner := &jsonScanner{source: utf16.Encode([]rune(source))}
	scanner.skipWhitespace()
	if scanner.pos < len(scanner.source) {
		switch scanner.source[scanner.pos] {
		case '{', '[':
			scanner.brackets = true
		}
	}
	if err := scanner.parseValue(); err != nil {
		return err
	}
	scanner.skipWhitespace()
	if scanner.pos >= len(scanner.source) {
		return nil
	}
	if scanner.brackets {
		return scanner.errorAt(scanner.pos, "Unexpected non-whitespace character after JSON")
	}
	return scanner.tokenErrorAt(scanner.valuePos)
}

func (s *jsonScanner) skipWhitespace() {
	for s.pos < len(s.source) {
		switch s.source[s.pos] {
		case ' ', '\t', '\n', '\r':
			s.pos++
		default:
			return
		}
	}
}

func (s *jsonScanner) parseValue() *jsonScanError {
	if s.pos >= len(s.source) {
		return &jsonScanError{message: "Unexpected end of JSON input"}
	}
	s.valuePos = s.pos
	switch char := s.source[s.pos]; {
	case char == '{':
		return s.parseObject()
	case char == '[':
		return s.parseArray()
	case char == '"':
		return s.parseString()
	case char == '-' || (char >= '0' && char <= '9'):
		return s.parseNumber()
	case char == 't':
		return s.parseLiteral("true")
	case char == 'f':
		return s.parseLiteral("false")
	case char == 'n':
		return s.parseLiteral("null")
	default:
		return s.valueError(s.pos)
	}
}

func (s *jsonScanner) parseObject() *jsonScanError {
	s.depth++
	defer func() { s.depth-- }()
	s.pos++
	s.skipWhitespace()
	if s.pos < len(s.source) && s.source[s.pos] == '}' {
		s.pos++
		return nil
	}
	for {
		s.skipWhitespace()
		if s.pos >= len(s.source) {
			return s.errorAt(s.pos, "Expected property name or '}' in JSON")
		}
		if s.source[s.pos] != '"' {
			if s.pos == s.valuePos+1 {
				return s.errorAt(s.pos, "Expected property name or '}' in JSON")
			}
			return s.errorAt(s.pos, "Expected double-quoted property name in JSON")
		}
		if err := s.parseString(); err != nil {
			return err
		}
		s.skipWhitespace()
		if s.pos >= len(s.source) {
			return s.errorAt(s.pos, "Expected ':' after property name in JSON")
		}
		if s.source[s.pos] != ':' {
			return s.errorAt(s.pos, "Expected ':' after property name in JSON")
		}
		s.pos++
		s.skipWhitespace()
		if err := s.parseValue(); err != nil {
			return err
		}
		s.skipWhitespace()
		if s.pos >= len(s.source) {
			return s.errorAt(s.pos, "Expected ',' or '}' after property value in JSON")
		}
		switch s.source[s.pos] {
		case ',':
			s.pos++
		case '}':
			s.pos++
			return nil
		default:
			return s.errorAt(s.pos, "Expected ',' or '}' after property value in JSON")
		}
	}
}

func (s *jsonScanner) parseArray() *jsonScanError {
	s.depth++
	defer func() { s.depth-- }()
	s.pos++
	s.skipWhitespace()
	if s.pos < len(s.source) && s.source[s.pos] == ']' {
		s.pos++
		return nil
	}
	for {
		s.skipWhitespace()
		if s.pos >= len(s.source) {
			return &jsonScanError{message: "Unexpected end of JSON input"}
		}
		if err := s.parseValue(); err != nil {
			return err
		}
		s.skipWhitespace()
		if s.pos >= len(s.source) {
			return s.errorAt(s.pos, "Expected ',' or ']' after array element in JSON")
		}
		switch s.source[s.pos] {
		case ',':
			s.pos++
		case ']':
			s.pos++
			return nil
		default:
			return s.errorAt(s.pos, "Expected ',' or ']' after array element in JSON")
		}
	}
}

func (s *jsonScanner) parseString() *jsonScanError {
	s.pos++
	for s.pos < len(s.source) {
		char := s.source[s.pos]
		switch {
		case char == '"':
			s.pos++
			return nil
		case char == '\\':
			s.pos++
			if s.pos >= len(s.source) {
				return s.valueErrorAt(s.pos, "Unterminated string in JSON")
			}
			switch s.source[s.pos] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				s.pos++
			case 'u':
				s.pos++
				for i := 0; i < 4; i++ {
					if s.pos >= len(s.source) {
						return s.valueErrorAt(s.pos, "Unterminated string in JSON")
					}
					if !isHex(s.source[s.pos]) {
						return s.valueErrorAt(s.pos, "Bad Unicode escape in JSON")
					}
					s.pos++
				}
			default:
				return s.valueErrorAt(s.pos, "Bad escaped character in JSON")
			}
		case char < 0x20:
			return s.valueErrorAt(s.pos, "Bad control character in string literal in JSON")
		default:
			s.pos++
		}
	}
	return s.valueErrorAt(s.pos, "Unterminated string in JSON")
}

func (s *jsonScanner) parseNumber() *jsonScanError {
	start := s.pos
	if s.source[s.pos] == '-' {
		s.pos++
		if s.pos >= len(s.source) || s.source[s.pos] < '0' || s.source[s.pos] > '9' {
			return s.valueErrorAt(s.pos, "No number after minus sign in JSON")
		}
	}
	digits := 0
	for s.pos < len(s.source) && s.source[s.pos] >= '0' && s.source[s.pos] <= '9' {
		if digits == 1 && s.source[start] == '0' {
			return s.valueErrorAt(s.pos, "Unexpected number in JSON")
		}
		digits++
		s.pos++
	}
	if s.pos < len(s.source) && s.source[s.pos] == '.' {
		s.pos++
		fraction := 0
		for s.pos < len(s.source) && s.source[s.pos] >= '0' && s.source[s.pos] <= '9' {
			fraction++
			s.pos++
		}
		if fraction == 0 {
			return s.valueErrorAt(s.pos, "Unterminated fractional number in JSON")
		}
	}
	if s.pos < len(s.source) && (s.source[s.pos] == 'e' || s.source[s.pos] == 'E') {
		s.pos++
		if s.pos < len(s.source) && (s.source[s.pos] == '+' || s.source[s.pos] == '-') {
			s.pos++
		}
		exponent := 0
		for s.pos < len(s.source) && s.source[s.pos] >= '0' && s.source[s.pos] <= '9' {
			exponent++
			s.pos++
		}
		if exponent == 0 {
			return s.valueErrorAt(s.pos, "Exponent part is missing a number in JSON")
		}
	}
	return nil
}

func (s *jsonScanner) parseLiteral(literal string) *jsonScanError {
	start := s.pos
	for i := 0; i < len(literal); i++ {
		if s.pos+i >= len(s.source) {
			return s.valueError(start)
		}
		char := s.source[s.pos+i]
		if char != uint16(literal[i]) {
			if isIdentifierChar(char) {
				return s.valueError(start)
			}
			return s.valueError(s.pos + i)
		}
	}
	s.pos += len(literal)
	return nil
}

func isIdentifierChar(char uint16) bool {
	switch {
	case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9':
		return true
	case char == '_' || char == '$':
		return true
	default:
		return false
	}
}

// valueError reports an unexpected token where no precise diagnostic exists.
func (s *jsonScanner) valueError(at int) *jsonScanError {
	return s.valueErrorAt(at, "")
}

func (s *jsonScanner) valueErrorAt(at int, reason string) *jsonScanError {
	if s.depth == 0 {
		return s.tokenErrorAt(s.valuePos)
	}
	if reason == "" {
		return s.tokenErrorAt(at)
	}
	return s.errorAt(at, reason)
}

func (s *jsonScanner) tokenErrorAt(pos int) *jsonScanError {
	if pos < 0 {
		return &jsonScanError{message: "Unexpected end of JSON input"}
	}
	if pos >= len(s.source) {
		pos = len(s.source) - 1
	}
	char := string(utf16.Decode(s.source[pos : pos+1]))
	return &jsonScanError{message: s.unexpectedTokenMessage(pos, char)}
}

func (s *jsonScanner) unexpectedTokenMessage(pos int, char string) string {
	length := len(s.source)
	var context string
	switch {
	case length <= jsonContextRadius*2:
		context = `"` + s.context(0, length) + `"`
	case pos < jsonContextRadius:
		context = `"` + s.context(0, jsonContextRadius) + `"...`
	case pos > length-jsonContextRadius:
		context = `..."` + s.context(length-jsonContextRadius, length) + `"`
	default:
		context = `..."` + s.context(pos-jsonContextRadius, pos+jsonContextRadius) + `"...`
	}
	return fmt.Sprintf("Unexpected token '%s', %s is not valid JSON", char, context)
}

func (s *jsonScanner) context(start, end int) string {
	if start < 0 {
		start = 0
	}
	if end > len(s.source) {
		end = len(s.source)
	}
	return string(utf16.Decode(s.source[start:end]))
}

func (s *jsonScanner) errorAt(pos int, reason string) *jsonScanError {
	line, column := s.lineColumn(pos)
	return &jsonScanError{
		message: fmt.Sprintf("%s at position %d (line %d column %d)", reason, pos, line, column),
	}
}

func (s *jsonScanner) lineColumn(pos int) (int, int) {
	line, column := 1, 1
	for i := 0; i < pos && i < len(s.source); i++ {
		if s.source[i] == '\n' {
			line++
			column = 1
			continue
		}
		column++
	}
	return line, column
}

func isHex(char uint16) bool {
	switch {
	case char >= '0' && char <= '9', char >= 'a' && char <= 'f', char >= 'A' && char <= 'F':
		return true
	default:
		return false
	}
}
