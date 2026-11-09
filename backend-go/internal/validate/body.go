package validate

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Field is one property of a decoded JSON body. It keeps the distinction
// between an absent property, a null and a non-string value, because
// class-validator treats all three differently.
type Field struct {
	name     string
	present  bool
	IsString bool
	IsNull   bool
	value    string
	skipped  bool
}

func (f Field) Present() bool { return f.present }

func (f Field) Value() string {
	if !f.IsString {
		return ""
	}
	return f.value
}

func (f Field) Skipped() bool { return f.skipped }

// Transform applies the legacy @Transform behaviour, which leaves non-string
// values untouched.
func (f Field) Transform(transform func(string) string) Field {
	if f.IsString {
		f.value = transform(f.value)
	}
	return f
}

// Optional mirrors @IsOptional: an absent or null value skips every other
// rule on the property. An empty string is still validated.
func (f Field) Optional() Field {
	if !f.present || f.IsNull {
		f.skipped = true
	}
	return f
}

type Body struct {
	object  Object
	fields  map[string]Field
	unknown []string
}

// Object is a decoded JSON object that remembers its key order, because the
// legacy whitelist errors are reported in payload order.
type Object struct {
	Keys   []string
	Values map[string]json.RawMessage
}

func NewBody(object Object, known ...string) *Body {
	allowed := make(map[string]bool, len(known))
	for _, name := range known {
		allowed[name] = true
	}
	body := &Body{object: object, fields: make(map[string]Field, len(known))}
	for _, name := range known {
		body.fields[name] = Field{name: name}
	}
	for _, name := range object.Keys {
		if !allowed[name] {
			body.unknown = append(body.unknown, name)
			continue
		}
		field := Field{name: name, present: true}
		raw := object.Values[name]
		var text string
		if isJSONNull(raw) {
			field.IsNull = true
		} else if err := json.Unmarshal(raw, &text); err == nil {
			field.IsString = true
			field.value = text
		}
		body.fields[name] = field
	}
	return body
}

func isJSONNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

func (b *Body) Unknown() []string { return b.unknown }

// String returns the declared property; unknown names yield an absent field.
func (b *Body) String(name string) Field {
	if field, ok := b.fields[name]; ok {
		return field
	}
	return Field{name: name}
}

// Raw returns the property as it arrived, for DTO properties that class-
// transformer coerces into a non-string type.
func (b *Body) Raw(name string) json.RawMessage {
	return b.object.Values[name]
}

func LowerTrim(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func Trim(raw string) string {
	return strings.TrimSpace(raw)
}
