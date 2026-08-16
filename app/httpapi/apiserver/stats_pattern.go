package apiserver

import (
	"regexp"
	"strings"
	"sync"
)

var statPatternCache sync.Map // pattern string -> *regexp.Regexp or nil (substring mode)

// matchStatPattern matches counter names against pattern.
// Plain text uses substring search; patterns with regex metacharacters use Go regexp automatically.
func matchStatPattern(name, pattern string) bool {
	if pattern == "" {
		return true
	}
	if !looksLikeRegexPattern(pattern) {
		return strings.Contains(name, pattern)
	}
	re := compileStatPattern(pattern)
	if re == nil {
		return strings.Contains(name, pattern)
	}
	return re.MatchString(name)
}

func looksLikeRegexPattern(pattern string) bool {
	for _, ch := range pattern {
		switch ch {
		case '(', ')', '|', '[', ']', '^', '$', '*', '+', '?', '\\', '{', '}':
			return true
		}
	}
	return false
}

func compileStatPattern(pattern string) *regexp.Regexp {
	if v, ok := statPatternCache.Load(pattern); ok {
		if v == nil {
			return nil
		}
		return v.(*regexp.Regexp)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		statPatternCache.Store(pattern, nil)
		return nil
	}
	statPatternCache.Store(pattern, re)
	return re
}
