package apperror

import (
	"strings"
	"testing"
)

func FuzzParseReason(f *testing.F) {
	f.Add("PS-404")
	f.Add("MY-SVC-422")
	f.Add("")
	f.Add("UNKNOWN")

	f.Fuzz(func(t *testing.T, reason string) {
		systemCode, code := parseReason(reason)

		if reason == "" {
			if systemCode != "" || code != 0 {
				t.Fatalf("empty reason should return zero values, got %q %d", systemCode, code)
			}
			return
		}

		if !strings.Contains(reason, "-") {
			if systemCode != reason {
				t.Fatalf("expected whole reason as system code, got %q", systemCode)
			}
			return
		}

		if systemCode == "" {
			t.Fatal("expected non-empty system code")
		}

		rebuilt := systemCode + "-" + strings.TrimPrefix(reason, systemCode+"-")
		if !strings.HasPrefix(reason, systemCode) {
			t.Fatalf("system code %q should be prefix of %q", systemCode, reason)
		}
		_ = rebuilt
		_ = code
	})
}
