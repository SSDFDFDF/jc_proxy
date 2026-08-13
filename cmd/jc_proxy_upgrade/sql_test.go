package main

import "testing"

func TestQuoteIdent(t *testing.T) {
	cases := map[string]string{
		"jc_proxy_upstream_keys":        `"jc_proxy_upstream_keys"`,
		"public.jc_proxy_upstream_keys": `"public"."jc_proxy_upstream_keys"`,
		" spaced ":                      `"spaced"`,
		`we"ird`:                        `"we""ird"`,
	}
	for in, want := range cases {
		if got := quoteIdent(in); got != want {
			t.Fatalf("quoteIdent(%q) = %q, want %q", in, got, want)
		}
	}
}
