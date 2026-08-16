package apiserver

import "testing"

func TestMatchStatPatternSubstring(t *testing.T) {
	if !matchStatPattern("inbound>>>a>>>traffic>>>uplink", "inbound>>>a>>>traffic>>>") {
		t.Fatal("expected substring match")
	}
	if matchStatPattern("outbound>>>x>>>traffic>>>uplink", "inbound>>>") {
		t.Fatal("expected no match")
	}
}

func TestMatchStatPatternRegexAuto(t *testing.T) {
	pattern := `inbound>>>new-in>>>traffic>>>(uplink|downlink)`
	if !matchStatPattern("inbound>>>new-in>>>traffic>>>uplink", pattern) {
		t.Fatal("expected uplink regex match")
	}
	if !matchStatPattern("inbound>>>new-in>>>traffic>>>downlink", pattern) {
		t.Fatal("expected downlink regex match")
	}
	if matchStatPattern("inbound>>>other>>>traffic>>>uplink", pattern) {
		t.Fatal("expected no match for other inbound")
	}
}

func TestMatchStatPatternInvalidRegexFallback(t *testing.T) {
	// Unclosed group falls back to substring.
	pattern := "foo(bar"
	if !matchStatPattern("prefix foo(bar suffix", pattern) {
		t.Fatal("expected substring fallback")
	}
}

func TestLooksLikeRegexPattern(t *testing.T) {
	if looksLikeRegexPattern("user>>>") {
		t.Fatal("plain substring should not look like regex")
	}
	if !looksLikeRegexPattern("a(b|c)") {
		t.Fatal("alternation should look like regex")
	}
}
