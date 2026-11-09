package validate

import (
	"net/netip"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	tldPattern = regexp.MustCompile(`(?i)^([a-z\x{00A1}-\x{00A8}\x{00AA}-\x{D7FF}\x{F900}-\x{FDCF}\x{FDF0}-\x{FFEF}]{2,}|xn[a-z0-9-]{2,})$`)
	fqdnLabel  = regexp.MustCompile(`(?i)^[a-z_\x{00a1}-\x{ffff}0-9-]+$`)
	fullWidth  = regexp.MustCompile(`[\x{ff01}-\x{ff5e}]`)
	allDigits  = regexp.MustCompile(`^[0-9]+$`)

	emailUserPart   = regexp.MustCompile("(?i)^[a-z0-9!#$%&'*+\\-/=?^_`{|}~\\x{00A1}-\\x{D7FF}\\x{F900}-\\x{FDCF}\\x{FDF0}-\\x{FFEF}]+$")
	quotedEmailUser = regexp.MustCompile(`(?i)^([\s\x{01}-\x{08}\x{0b}\x{0c}\x{0e}-\x{1f}\x{7f}\x{21}\x{23}-\x{5b}\x{5d}-\x{7e}\x{00a0}-\x{D7FF}\x{F900}-\x{FDCF}\x{FDF0}-\x{FFEF}]|(\\[\x{01}-\x{09}\x{0b}\x{0c}\x{0d}-\x{7f}\x{00a0}-\x{D7FF}\x{F900}-\x{FDCF}\x{FDF0}-\x{FFEF}]))*$`)
)

func isIP(value string, version int) bool {
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return false
	}
	switch version {
	case 4:
		return addr.Is4()
	case 6:
		return addr.Is6()
	default:
		return true
	}
}

func isFQDN(value string, requireTLD bool) bool {
	parts := strings.Split(value, ".")
	tld := parts[len(parts)-1]
	if requireTLD {
		if len(parts) < 2 {
			return false
		}
		if !tldPattern.MatchString(tld) {
			return false
		}
		if strings.ContainsAny(tld, " \t\n\v\f\r") {
			return false
		}
	}
	if allDigits.MatchString(tld) {
		return false
	}
	for _, part := range parts {
		if len(part) > 63 {
			return false
		}
		if !fqdnLabel.MatchString(part) {
			return false
		}
		if fullWidth.MatchString(part) {
			return false
		}
		if strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
			return false
		}
		if strings.Contains(part, "_") {
			return false
		}
	}
	return true
}

func byteLength(value string) int {
	return len(value)
}

func runeLength(value string) int {
	return utf8.RuneCountInString(value)
}

// IsEmail reproduces validator.js isEmail with its default options, which is
// what class-validator's @IsEmail() delegates to.
func IsEmail(value string) bool {
	if runeLength(value) > 254 {
		return false
	}
	at := strings.LastIndex(value, "@")
	if at < 0 {
		return false
	}
	user, domain := value[:at], value[at+1:]
	if byteLength(user) > 64 || byteLength(domain) > 254 {
		return false
	}
	if !isFQDN(domain, true) {
		return false
	}
	if strings.HasPrefix(user, `"`) && strings.HasSuffix(user, `"`) {
		inner := ""
		if len(user) >= 2 {
			inner = user[1 : len(user)-1]
		}
		return quotedEmailUser.MatchString(inner)
	}
	for _, part := range strings.Split(user, ".") {
		if !emailUserPart.MatchString(part) {
			return false
		}
	}
	return true
}
