package resin

import "testing"

func TestBuildReverseURL(t *testing.T) {
	cfg := RuntimeConfig{
		ResinURL: "http://127.0.0.1:2260/my-token",
		Platform: "Default",
	}
	got, err := BuildReverseURL("https://api.openai.com/v1/chat/completions?stream=true", cfg)
	if err != nil {
		t.Fatalf("BuildReverseURL error: %v", err)
	}
	want := "http://127.0.0.1:2260/my-token/Default/https/api.openai.com/v1/chat/completions?stream=true"
	if got != want {
		t.Fatalf("reverse url mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestBuildAccount(t *testing.T) {
	got := BuildAccount("openai", "test-key")
	if got == "" || got[:11] != "jcp:openai-" {
		t.Fatalf("unexpected account: %s", got)
	}
	if len(got) != len("jcp:openai-")+12 {
		t.Fatalf("account hash length mismatch: %s", got)
	}
	if got != BuildAccount("openai", "test-key") {
		t.Fatalf("account hash should be stable")
	}
}
