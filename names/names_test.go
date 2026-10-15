package names_test

import (
	"math"
	"testing"

	"swiftbon.com/shared/names"
)

func TestNormalise(t *testing.T) {
	tests := map[string]string{
		"":                           "",
		"   ":                        "",
		"Ogunlesi Haulage Ltd":       "OGUNLESI HAULAGE LTD",
		"  adaeze   okonkwo ":        "ADAEZE OKONKWO",
		"José Ñandú Çelik":           "JOSE NANDU CELIK",
		"Zoë Müller":                 "ZOE MULLER",
		"O'Brien":                    "OBRIEN",
		"O’Brien":                    "OBRIEN",
		"Anne-Marie":                 "ANNE MARIE",
		"Smith & Sons":               "SMITH AND SONS",
		"Smith&Sons":                 "SMITH AND SONS",
		"Acme L.L.C.":                "ACME LLC",
		"Ltd.":                       "LTD",
		"ＡＢＣ　１２３":                    "ABC 123",
		"ﬁnance":                     "FINANCE",
		"Chidinma\tOkafor\nVentures": "CHIDINMA OKAFOR VENTURES",
		"(Acme), Inc;":               "ACME INC",
	}
	for in, want := range tests {
		if got := names.Normalise(in); got != want {
			t.Errorf("Normalise(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormaliseIdempotent(t *testing.T) {
	for _, s := range []string{"José O'Brien-Smith & Co.", "Ogunlesi Haulage Ltd", "ＡＢＣ"} {
		once := names.Normalise(s)
		if twice := names.Normalise(once); twice != once {
			t.Errorf("not idempotent: %q -> %q -> %q", s, once, twice)
		}
	}
}

// The pairs from the dashboard mockup, at the 0.85 placeholder threshold.
func TestMatchMockupPairs(t *testing.T) {
	const threshold = 0.85
	tests := []struct {
		name string
		a, b string
		kind names.Kind
		want bool
	}{
		{"company suffix spelling", "OGUNLESI HAULAGE LIMITED", "Ogunlesi Haulage Ltd", names.Company, true},
		{"different person", "ADAEZE OKONKWO", "ADAEZE NWOSU", names.Person, false},
		{"different company", "CHIDINMA OKAFOR VENTURES", "Ogunlesi Haulage Ltd", names.Company, false},
		{"person word order", "OKONKWO ADAEZE", "Adaeze Okonkwo", names.Person, true},
		{"person diacritics", "José Müller", "JOSE MULLER", names.Person, true},
		{"person extra name is not a perfect match", "ADAEZE OKONKWO", "ADAEZE OKONKWO NWOSU", names.Person, false},
		{"plc phrase", "Dangote Cement Public Limited Company", "DANGOTE CEMENT PLC", names.Company, true},
		{"incorporated", "Acme Incorporated", "ACME INC", names.Company, true},
		{"different legal form", "Acme Foundation", "Acme Ltd", names.Company, false},
		{"one letter typo", "ADAEZE OKONKWO", "ADAEZE OKONKWA", names.Person, true},
		{"repeated word counts once", "ADAEZE ADAEZE OKONKWO", "ADAEZE OKONKWO", names.Person, true},
	}
	for _, tc := range tests {
		score, ok := names.Match(tc.a, tc.b, tc.kind, threshold)
		if ok != tc.want {
			t.Errorf("%s: Match(%q, %q) = %.3f, %v; want matched=%v", tc.name, tc.a, tc.b, score, ok, tc.want)
		}
	}
}

func TestSuffixEquivalenceOnlyForCompanies(t *testing.T) {
	// As a person's name, "Ltd" and "Limited" are just different words.
	person, _ := names.Match("Ogunlesi Haulage Ltd", "Ogunlesi Haulage Limited", names.Person, 0.99)
	company, _ := names.Match("Ogunlesi Haulage Ltd", "Ogunlesi Haulage Limited", names.Company, 0.99)
	if company != 1 || person >= 1 {
		t.Errorf("company=%v person=%v", company, person)
	}
}

func TestScoreProperties(t *testing.T) {
	pairs := [][2]string{
		{"ADAEZE OKONKWO", "ADAEZE NWOSU"},
		{"Ogunlesi Haulage Ltd", "OGUNLESI HAULAGE LIMITED"},
		{"A", "B"},
		{"Chidinma Okafor Ventures", "Ogunlesi Haulage Ltd"},
	}
	for _, p := range pairs {
		ab := names.Score(p[0], p[1], names.Company)
		ba := names.Score(p[1], p[0], names.Company)
		if ab != ba {
			t.Errorf("not symmetric: %v vs %v for %v", ab, ba, p)
		}
		if ab < 0 || ab > 1 {
			t.Errorf("score %v out of range", ab)
		}
	}
	if s := names.Score("Adaeze Okonkwo", "ADAEZE  OKONKWO", names.Person); s != 1 {
		t.Errorf("identical after normalisation scored %v", s)
	}
}

func TestEmptyNames(t *testing.T) {
	for _, p := range [][2]string{{"", ""}, {"", "A"}, {"A", ""}, {"  ", "..."}} {
		if s, ok := names.Match(p[0], p[1], names.Person, 0.5); s != 0 || ok {
			t.Errorf("Match(%q, %q) = %v, %v", p[0], p[1], s, ok)
		}
	}
}

func TestBadThresholdFailsClosed(t *testing.T) {
	for _, th := range []float64{0, -1, 1.01, math.NaN(), math.Inf(1)} {
		if _, ok := names.Match("Adaeze Okonkwo", "Adaeze Okonkwo", names.Person, th); ok {
			t.Errorf("threshold %v matched", th)
		}
	}
	if _, ok := names.Match("Adaeze Okonkwo", "Adaeze Okonkwo", names.Person, 1); !ok {
		t.Error("identical names should match at threshold 1")
	}
}
