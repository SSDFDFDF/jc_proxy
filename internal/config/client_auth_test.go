package config

import (
	"encoding/json"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestClientAuthDefaultsAndRoundTrip(t *testing.T) {
	for _, provider := range []string{"generic", "openai", "anthropic", "gemini", "deepseek", "azure_openai", "aggregate"} {
		for _, tc := range []struct {
			name    string
			auth    string
			enabled bool
			keys    []string
		}{
			{"omitted", "", true, nil},
			{"null", `null`, true, nil},
			{"empty", `{}`, true, nil},
			{"keys-default-enabled", `{"keys":[" client ","client", " "]}`, true, []string{"client"}},
			{"null-enabled", `{"enabled":null,"keys":["client"]}`, true, []string{"client"}},
			{"enabled-no-keys", `{"enabled":true}`, true, nil},
			{"disabled-no-keys", `{"enabled":false}`, true, nil},
			{"disabled-blank-keys", `{"enabled":false,"keys":[" ","\t"]}`, true, nil},
			{"explicitly-disabled", `{"enabled":false,"keys":["client"]}`, false, []string{"client"}},
		} {
			t.Run(provider+"/"+tc.name, func(t *testing.T) {
				payload := `{"provider":"` + provider + `","upstream":{"base_url":"https://example.invalid"}`
				if tc.auth != "" {
					payload += `,"client_auth":` + tc.auth
				}
				payload += `}`
				for _, decode := range []func([]byte, any) error{json.Unmarshal, yaml.Unmarshal} {
					var vendor VendorConfig
					if err := decode([]byte(payload), &vendor); err != nil {
						t.Fatal(err)
					}
					cfg := &Config{Vendors: VendorsFromMap(map[string]VendorConfig{"test": vendor})}
					if err := cfg.PrepareAndValidate(); err != nil {
						t.Fatal(err)
					}
					clone, err := cfg.Clone()
					if err != nil {
						t.Fatal(err)
					}
					for _, c := range []*Config{cfg, clone} {
						auth := c.Vendors[0].ClientAuth
						if auth.Enabled == nil || auth.IsEnabled() != tc.enabled || !slices.Equal(auth.Keys, tc.keys) {
							t.Fatalf("auth = %+v (enabled=%v), want enabled=%v keys=%v", auth, auth.IsEnabled(), tc.enabled, tc.keys)
						}
						encoded, err := json.Marshal(c.Vendors[0])
						if err != nil {
							t.Fatal(err)
						}
						var roundTrip VendorConfig
						if err := json.Unmarshal(encoded, &roundTrip); err != nil || roundTrip.ClientAuth.IsEnabled() != tc.enabled {
							t.Fatalf("JSON round trip lost enabled=%v: %s (%v)", tc.enabled, encoded, err)
						}
					}
				}
			})
		}
	}
}
