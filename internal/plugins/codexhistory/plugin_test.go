package codexhistory

import (
	"bytes"
	"context"
	"testing"

	"jc_proxy/internal/config"
	"jc_proxy/internal/plugins"
)

func TestPreservesLargeNumbersAndOriginalBody(t *testing.T) {
	p := New(config.PluginConfig{Name: "history"})
	raw := []byte(`{"metadata":{"number":9007199254740993},"input":[{"role":"user","content":"hi","id":"remove"}]}`)
	original := string(raw)
	out, err := p.Transform(context.Background(), plugins.Request{Body: raw})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Body, []byte("9007199254740993")) || bytes.Contains(out.Body, []byte("remove")) || string(raw) != original {
		t.Fatalf("lossy transform: %s", out.Body)
	}
}
