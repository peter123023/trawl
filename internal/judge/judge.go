package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// Config describes an OpenAI-compatible chat endpoint. Anything self-hosted
// (vLLM, Ollama's OpenAI shim, OneAPI) works as long as it speaks
// /chat/completions.
type Config struct {
	BaseURL      string
	APIKey       string
	Model        string
	AllowPrivate bool
	Timeout      time.Duration
}

type Client struct {
	cfg  Config
	http *http.Client
}

var ErrNotConfigured = errors.New("judge not configured: set TRAWL_JUDGE_BASE_URL / TRAWL_JUDGE_API_KEY")

// ValidateURL checks an outbound URL: only http(s), with private and loopback
// addresses denied by default. Judge and price sync share this policy.
func ValidateURL(raw string, allowPrivate bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("url scheme must be http(s), got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("url has no host")
	}
	if !allowPrivate && isPrivateHost(u.Hostname()) {
		return nil, fmt.Errorf("url points at a private address %q; set TRAWL_ALLOW_PRIVATE_FETCH=1 to allow", u.Hostname())
	}
	return u, nil
}

func New(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" || cfg.APIKey == "" {
		return nil, ErrNotConfigured
	}
	if _, err := ValidateURL(cfg.BaseURL, cfg.AllowPrivate); err != nil {
		return nil, fmt.Errorf("judge base url: %w", err)
	}
	if cfg.Model == "" {
		cfg.Model = "gpt-4o-mini"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	return &Client{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}, nil
}

func (c *Client) Model() string { return c.cfg.Model }

func isPrivateHost(host string) bool {
	h := strings.ToLower(host)
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") {
		return true
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

const systemPrompt = `你是一名严格的 LLM 输出评测员。
只输出一个 JSON 对象，不要输出任何解释性文字或 markdown 代码块：
{"score": 0, "reason": ""}
score 取值 0 到 3 的整数：
3 = 完全正确且忠实于输入与上下文
2 = 基本正确，但存在次要瑕疵
1 = 有明显错误，但方向尚可
0 = 错误、答非所问或编造
reason 用一句中文说明判定依据，不超过 60 字。`

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type verdict struct {
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

// Result is one judgement. Raw keeps the model's raw content so callers can
// still inspect it when parsing fails.
type Result struct {
	Score  float64
	Reason string
	Raw    string
}

// Score asks the configured model to grade one generation. The caller owns
// persistence; this package only returns the verdict.
func (c *Client) Score(ctx context.Context, input, output string) (Result, error) {
	if strings.TrimSpace(output) == "" {
		return Result{}, errors.New("judge: empty output, nothing to grade")
	}
	user := fmt.Sprintf("【输入】\n%s\n\n【输出】\n%s", trim(input, 8000), trim(output, 8000))
	body, err := json.Marshal(chatRequest{
		Model:       c.cfg.Model,
		Temperature: 0.1,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: user},
		},
	})
	if err != nil {
		return Result{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	res, err := c.http.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("judge request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return Result{}, fmt.Errorf("judge upstream returned %s", res.Status)
	}

	var payload chatResponse
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return Result{}, fmt.Errorf("judge decode: %w", err)
	}
	if payload.Error != nil {
		return Result{}, fmt.Errorf("judge upstream error: %s", payload.Error.Message)
	}
	if len(payload.Choices) == 0 {
		return Result{}, errors.New("judge: empty choices")
	}

	content := payload.Choices[0].Message.Content
	v, err := parseVerdict(content)
	if err != nil {
		return Result{}, err
	}
	if v.Score < 0 {
		v.Score = 0
	}
	if v.Score > 3 {
		v.Score = 3
	}
	reason := strings.TrimSpace(v.Reason)
	if reason == "" {
		reason = "模型未给出理由"
	}
	return Result{Score: v.Score, Reason: reason, Raw: content}, nil
}

func parseVerdict(content string) (verdict, error) {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return verdict{}, errors.New("judge: response is not JSON")
	}
	var v verdict
	if err := json.Unmarshal([]byte(content[start:end+1]), &v); err != nil {
		return verdict{}, fmt.Errorf("judge: invalid JSON verdict: %w", err)
	}
	return v, nil
}

func trim(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	return string(runes[:limit]) + "…"
}
