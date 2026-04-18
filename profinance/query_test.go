package profinance

import "testing"

func TestValidateFullCodeRejectsNonDigitSuffix(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"shABCDEF", "sz12 456", "bj12a456"} {
		if _, err := validateFullCode(input); err == nil {
			t.Fatalf("validateFullCode(%q) should fail", input)
		} else if queryErr, ok := err.(*QueryError); !ok || queryErr.ErrorCode != "INVALID_ARGUMENT" {
			t.Fatalf("validateFullCode(%q) error = %#v, want INVALID_ARGUMENT", input, err)
		}
	}
}
