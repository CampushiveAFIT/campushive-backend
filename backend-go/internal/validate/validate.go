package validate

import (
	"fmt"
	"regexp"
	"strings"
)

type Errors []string

func (e Errors) Err() error {
	if len(e) == 0 {
		return nil
	}
	return fmt.Errorf("validation failed")
}

func (e *Errors) Add(message string) {
	*e = append(*e, message)
}

func (e *Errors) Addf(format string, args ...any) {
	e.Add(fmt.Sprintf(format, args...))
}

// Whitelist reports properties the DTO does not declare, in payload order,
// exactly like class-validator's forbidNonWhitelisted.
func (e *Errors) Whitelist(unknown []string) {
	for _, name := range unknown {
		e.Addf("property %s should not exist", name)
	}
}

// IsString mirrors @IsString. Anything that is not a JSON string fails,
// including an absent property.
func (e *Errors) IsString(field Field) {
	if field.Skipped() {
		return
	}
	if !field.IsString {
		e.Addf("%s must be a string", field.name)
	}
}

// MinLength mirrors @MinLength, which only ever passes for strings.
func (e *Errors) MinLength(field Field, min int, custom string) {
	if field.Skipped() {
		return
	}
	if field.IsString && len([]rune(field.value)) >= min {
		return
	}
	if custom != "" {
		e.Add(custom)
		return
	}
	e.Addf("%s must be longer than or equal to %d characters", field.name, min)
}

// MaxLength mirrors @MaxLength, which only ever passes for strings.
func (e *Errors) MaxLength(field Field, max int) {
	if field.Skipped() {
		return
	}
	if field.IsString && len([]rune(field.value)) <= max {
		return
	}
	e.Addf("%s must be shorter than or equal to %d characters", field.name, max)
}

// Matches mirrors @Matches, which only ever passes for strings.
func (e *Errors) Matches(field Field, pattern *regexp.Regexp, custom string) {
	if field.Skipped() {
		return
	}
	if field.IsString && pattern.MatchString(field.value) {
		return
	}
	if custom != "" {
		e.Add(custom)
		return
	}
	e.Addf("%s must match %s", field.name, pattern)
}

// Email mirrors @IsEmail. A custom message replaces the default.
func (e *Errors) Email(field Field, custom string) {
	if field.Skipped() {
		return
	}
	if field.IsString && IsEmail(field.value) {
		return
	}
	if custom != "" {
		e.Add(custom)
		return
	}
	e.Addf("%s must be an email", field.name)
}

// URL mirrors @IsUrl({ require_protocol: true }).
func (e *Errors) URL(field Field) {
	if field.Skipped() {
		return
	}
	if field.IsString && IsURL(field.value) {
		return
	}
	e.Addf("%s must be a URL address", field.name)
}

// StrongPassword mirrors the NestJS PASSWORD_PATTERN
// /^(?=.*[a-z])(?=.*[A-Z])(?=.*\d).{8,}$/. Go has no look-ahead, so the three
// character classes and the length are checked directly.
func (e *Errors) StrongPassword(field Field, custom string) {
	if field.Skipped() {
		return
	}
	if field.IsString && StrongPassword(field.value) {
		return
	}
	e.Add(custom)
}

// UUID mirrors @IsUUID(version): a non-string fails the rule, and the message
// replaces the default ("$property must be a UUID") whenever the DTO declared
// one.
func (e *Errors) UUID(field Field, version int, message string) {
	if field.Skipped() {
		return
	}
	if field.IsString && IsUUID(field.value, version) {
		return
	}
	if message == "" {
		e.Addf("%s must be a UUID", field.name)
		return
	}
	e.Add(message)
}

// Enum mirrors @IsEnum: only the declared values pass, and a non-string fails
// too, because isEnum tests membership rather than type.
func (e *Errors) Enum(field Field, values []string, message string) {
	if field.Skipped() {
		return
	}
	if field.IsString && contains(values, field.value) {
		return
	}
	if message == "" {
		e.Addf("%s must be one of the following values: %s", field.name, strings.Join(values, ", "))
		return
	}
	e.Add(message)
}

// Text mirrors @IsString @MinLength @MaxLength. class-validator applies
// property decorators bottom-up, so the recorded messages run MaxLength,
// MinLength, IsString.
func (e *Errors) Text(field Field, min, max int, minMessage string) {
	e.MaxLength(field, max)
	e.MinLength(field, min, minMessage)
	e.IsString(field)
}

// TextMatch mirrors @IsString @MinLength @MaxLength @Matches.
func (e *Errors) TextMatch(field Field, min, max int, pattern *regexp.Regexp, matchMessage string) {
	e.Matches(field, pattern, matchMessage)
	e.MaxLength(field, max)
	e.MinLength(field, min, "")
	e.IsString(field)
}

// TextOnlyMax mirrors @IsString @MaxLength.
func (e *Errors) TextOnlyMax(field Field, max int) {
	e.MaxLength(field, max)
	e.IsString(field)
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

var (
	// uuidV4 matches the regex class-validator uses for @IsUUID('4').
	uuidV4 = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	// uuidAll matches NestJS ParseUUIDPipe's "all" pattern, which accepts any
	// version nibble.
	uuidAll = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// IsUUID reports whether value is a UUID of the requested version. version 4
// mirrors @IsUUID('4'); version 0 mirrors ParseUUIDPipe, which accepts any
// version.
func IsUUID(value string, version int) bool {
	if version == 4 {
		return uuidV4.MatchString(value)
	}
	return uuidAll.MatchString(value)
}

func StrongPassword(value string) bool {
	if len([]rune(value)) < 8 {
		return false
	}
	var hasLower, hasUpper, hasDigit bool
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= '0' && r <= '9':
			hasDigit = true
		}
	}
	return hasLower && hasUpper && hasDigit
}
