package judge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScoreParsesWrappedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer test-key") {
			t.Errorf("missing bearer auth")
		}
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(req.Messages) != 2 || req.Messages[0].Role != "system" {
			t.Errorf("unexpected messages: %+v", req.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		content := "```json\n{\"score\":2,\"reason\":\"基本正确，漏了时效\"}\n```"
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": content}},
			},
		})
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL + "/", APIKey: "test-key", Model: "m", AllowPrivate: true})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	res, err := c.Score(context.Background(), "退款多久到账？", "7 个工作日。")
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if res.Score != 2 {
		t.Errorf("score = %v", res.Score)
	}
	if res.Reason != "基本正确，漏了时效" {
		t.Errorf("reason = %q", res.Reason)
	}
	if !strings.Contains(res.Raw, "score") {
		t.Errorf("raw response not preserved: %q", res.Raw)
	}
}

func TestNewRejectsPrivateAddressByDefault(t *testing.T) {
	if _, err := New(Config{BaseURL: "http://127.0.0.1:9/v1", APIKey: "k"}); err == nil {
		t.Fatal("expected private address to be rejected")
	}
	if _, err := New(Config{BaseURL: "http://localhost:9/v1", APIKey: "k"}); err == nil {
		t.Fatal("expected localhost to be rejected")
	}
	if _, err := New(Config{BaseURL: "file:///etc/passwd", APIKey: "k"}); err == nil {
		t.Fatal("expected non-http scheme to be rejected")
	}
}

func TestScoreWithoutConfig(t *testing.T) {
	if _, err := New(Config{}); err != ErrNotConfigured {
		t.Fatalf("err = %v", err)
	}
}

func TestParseVerdictGarbage(t *testing.T) {
	if _, err := parseVerdict("不是 JSON"); err == nil {
		t.Fatal("expected parse error")
	}
}
