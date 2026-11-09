package validate

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	protocolPattern = regexp.MustCompile(`(?i)^([a-z][a-z0-9+\-.]*):`)
	authPattern     = regexp.MustCompile(`^[a-zA-Z0-9\-_.%:]*$`)
	encodedPattern  = regexp.MustCompile(`%[0-9a-fA-F]{2}`)
	wrappedIPv6     = regexp.MustCompile(`^\[([^\]]+)\](?::([0-9]+))?$`)
	portPattern     = regexp.MustCompile(`^[0-9]+$`)
	spaceOrAngle    = regexp.MustCompile(`[\s<>]`)
)

var allowedProtocols = map[string]bool{"http": true, "https": true, "ftp": true}

// IsURL reproduces @IsUrl({ require_protocol: true }), i.e. validator.js
// isURL with that single option and every other default left in place.
func IsURL(value string) bool {
	if value == "" || spaceOrAngle.MatchString(value) {
		return false
	}
	if strings.HasPrefix(value, "mailto:") {
		return false
	}
	if len(value) > 2084 {
		return false
	}

	rest := value
	if index := strings.Index(rest, "#"); index >= 0 {
		rest = rest[:index]
	}
	if index := strings.Index(rest, "?"); index >= 0 {
		rest = rest[:index]
	}

	hadProtocol := false
	if match := protocolPattern.FindStringSubmatch(rest); match != nil {
		afterColon := rest[len(match[0]):]
		if !strings.HasPrefix(afterColon, "//") {
			beforeSlash := afterColon
			if index := strings.Index(beforeSlash, "/"); index >= 0 {
				beforeSlash = beforeSlash[:index]
			}
			at := strings.Index(beforeSlash, "@")
			switch {
			case at >= 0:
				beforeAt := beforeSlash[:at]
				if authPattern.MatchString(beforeAt) && !encodedPattern.MatchString(beforeAt) {
					return false
				}
				if !allowedProtocols[strings.ToLower(match[1])] {
					return false
				}
				rest = afterColon
				hadProtocol = true
			case len(afterColon) > 0 && afterColon[0] >= '0' && afterColon[0] <= '9':
				return false
			default:
				if !allowedProtocols[strings.ToLower(match[1])] {
					return false
				}
				rest = afterColon
				hadProtocol = true
			}
		} else {
			if !allowedProtocols[strings.ToLower(match[1])] {
				return false
			}
			rest = afterColon
			hadProtocol = true
		}
	} else {
		return false
	}

	if strings.HasPrefix(rest, "//") {
		if !hadProtocol {
			return false
		}
		rest = rest[2:]
	}
	if rest == "" {
		return false
	}

	head := rest
	if index := strings.Index(rest, "/"); index >= 0 {
		head = rest[:index]
	}

	hostname := head
	if strings.Count(head, "@") > 1 {
		return false
	}
	if at := strings.Index(head, "@"); at >= 0 {
		auth := head[:at]
		if auth == "" {
			return false
		}
		parts := strings.Split(auth, ":")
		if len(parts) > 2 {
			return false
		}
		if len(parts) == 2 && parts[0] == "" && parts[1] == "" {
			return false
		}
		hostname = head[at+1:]
	}

	host := hostname
	port := ""
	ipv6 := ""
	if match := wrappedIPv6.FindStringSubmatch(hostname); match != nil {
		ipv6 = match[1]
		port = match[2]
	} else {
		if index := strings.Index(hostname, ":"); index >= 0 {
			host = hostname[:index]
			port = hostname[index+1:]
		}
	}
	if port != "" {
		if !portPattern.MatchString(port) {
			return false
		}
		number, err := strconv.Atoi(port)
		if err != nil || number <= 0 || number > 65535 {
			return false
		}
	}
	if host == "" && ipv6 == "" {
		return false
	}
	if ipv6 != "" {
		return isIP(ipv6, 6)
	}
	return isIP(host, 0) || isFQDN(host, true)
}
