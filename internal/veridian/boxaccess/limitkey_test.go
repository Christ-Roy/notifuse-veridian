package boxaccess

import "testing"

func TestLimitKey(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7":             "203.0.113.7",
		"::ffff:203.0.113.7":      "203.0.113.7",
		"2a02:c207:1:2::1":        "2a02:c207:1:2::/64",
		"2a02:c207:1:2:aa:bb:c:d": "2a02:c207:1:2::/64",
		"2a02:c207:1:3::1":        "2a02:c207:1:3::/64",
		"fe80::1%eth0":            "fe80::/64",
		"":                        "",
		"pas une adresse":         "pas une adresse",
	} {
		if got := LimitKey(in); got != want {
			t.Errorf("%q : %q, attendu %q", in, got, want)
		}
	}
}
