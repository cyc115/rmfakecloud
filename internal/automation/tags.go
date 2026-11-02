package automation

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// NoAutomationTag the opt-out tag, as displayed
const NoAutomationTag = "no automation"

var tagSeparators = regexp.MustCompile(`[\s_\-]+`)

// NormalizeTag lowercases and collapses whitespace, '-' and '_' runs to one space
func NormalizeTag(tag string) string {
	return strings.TrimSpace(tagSeparators.ReplaceAllString(strings.ToLower(tag), " "))
}

// NormalizePageTag a page tag as matched: the same as document tags
// (NormalizeTag), so "To-Do", "to_do" and "to do" are one tag
func NormalizePageTag(tag string) string {
	return NormalizeTag(tag)
}

// IsNoAutomationTag reports whether tag is the opt-out tag in any spelling
func IsNoAutomationTag(tag string) bool {
	return NormalizeTag(tag) == NoAutomationTag
}

// CompilePattern compiles a name pattern, case-insensitive.
// "/re/" is an RE2 regular expression (unanchored); anything else is a glob
// matched against the whole name, where * is any run and ? one character.
func CompilePattern(p string) (*regexp.Regexp, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return nil, errors.New("pattern cannot be empty")
	}
	if len(p) >= 2 && strings.HasPrefix(p, "/") && strings.HasSuffix(p, "/") {
		re, err := regexp.Compile("(?i)" + p[1:len(p)-1])
		if err != nil {
			return nil, fmt.Errorf("invalid regular expression: %v", err)
		}
		return re, nil
	}
	var sb strings.Builder
	sb.WriteString("(?is)^")
	for _, r := range p {
		switch r {
		case '*':
			sb.WriteString(".*")
		case '?':
			sb.WriteString(".")
		default:
			sb.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	sb.WriteString("$")
	return regexp.Compile(sb.String())
}
