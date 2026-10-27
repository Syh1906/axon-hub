package objects

import "testing"

func TestMatchModelID(t *testing.T) {
	for _, tt := range []struct {
		name           string
		kind           ModelMatchType
		pattern, model string
		want, invalid  bool
	}{
		{"exact", ModelMatchExact, "gpt-6-luna", "gpt-6-luna", true, false},
		{"exact rejects extension", ModelMatchExact, "gpt-6-luna", "gpt-6-luna-latest", false, false},
		{"prefix", ModelMatchPrefix, "gpt-", "gpt-6.1-luna", true, false},
		{"literal prefix", ModelMatchPrefix, "gpt-.*", "gpt-6-luna", false, false},
		{"suffix", ModelMatchSuffix, "-luna", "gpt-6.1-luna", true, false},
		{"suffix rejects trailing text", ModelMatchSuffix, "-luna", "gpt-6-luna-preview", false, false},
		{"regex", ModelMatchRegex, "^gpt-.*-luna$", "gpt-6.1-luna", true, false},
		{"regex whole ID", ModelMatchRegex, "gpt-.*-luna", "vendor/gpt-6-luna", false, false},
		{"invalid regex", ModelMatchRegex, "[", "gpt-6-luna", false, true},
		{"empty rule", ModelMatchSuffix, "", "gpt-6-luna", false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MatchModelID(tt.kind, tt.pattern, tt.model)
			if got != tt.want || (err != nil) != tt.invalid {
				t.Fatalf("MatchModelID() = %v, %v; want %v, invalid=%v", got, err, tt.want, tt.invalid)
			}
		})
	}
}
