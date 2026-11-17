package ingest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/trawlhq/trawl/internal/store"
)

// ---------- shared helpers ----------

func jsonText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return ""
	}
	out := buf.String()
	if out == "null" {
		return ""
	}
	return out
}

var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.999Z0700",
	"2006-01-02 15:04:05.999",
	"2006-01-02T15:04:05",
}

func parseTime(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n > 0 && n < 1e12 {
			return n * 1000, true
		}
		return n, true
	}
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().UnixMilli(), true
		}
	}
	return 0, false
}

func usageJSON(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	out := map[string]int64{}
	var total int64
	for k, v := range m {
		switch n := v.(type) {
		case float64:
			out[k] = int64(n)
			if k != "total" {
				total += int64(n)
			}
		case json.Number:
			if i, err := n.Int64(); err == nil {
				out[k] = i
				if k != "total" {
					total += i
				}
			}
		}
	}
	if _, ok := out["total"]; !ok && total > 0 {
		out["total"] = total
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// mergeUsage combines both usage sources: usageDetails wins and usage fills gaps.
func mergeUsage(legacy, modern map[string]any) map[string]any {
	if len(modern) == 0 {
		return legacy
	}
	if len(legacy) == 0 {
		return modern
	}
	out := make(map[string]any, len(legacy)+len(modern))
	for k, v := range legacy {
		out[k] = v
	}
	for k, v := range modern {
		out[k] = v
	}
	return out
}

func costJSON(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	out := map[string]float64{}
	var total float64
	for k, v := range m {
		switch n := v.(type) {
		case float64:
			out[k] = n
			if k != "total" {
				total += n
			}
		case json.Number:
			if f, err := n.Float64(); err == nil {
				out[k] = f
				if k != "total" {
					total += f
				}
			}
		}
	}
	if _, ok := out["total"]; !ok && total > 0 {
		out["total"] = total
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// computeCost derives cost_details from usage + model price when the SDK did
// not send them. Missing price => no cost, never a guessed number.
func computeCost(model string, usage map[string]any, prices map[string]store.Price) string {
	if model == "" || len(usage) == 0 {
		return ""
	}
	p, ok := prices[strings.ToLower(model)]
	if !ok {
		return ""
	}
	var in, out float64
	if v, ok := usage["input"].(float64); ok {
		in = v
	}
	if v, ok := usage["output"].(float64); ok {
		out = v
	}
	if in == 0 && out == 0 {
		return ""
	}
	m := map[string]float64{
		"input":  in * p.InputPer1k / 1000,
		"output": out * p.OutputPer1k / 1000,
	}
	m["total"] = m["input"] + m["output"]
	b, _ := json.Marshal(m)
	return string(b)
}

func tagsJSON(tags []string) string {
	if len(tags) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(tags)
	return string(b)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func defaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func ptrInt(v int64) *int64 { return &v }

// ---------- JSON ingestion ----------

type ingestEvent struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Body      json.RawMessage `json:"body"`
}

type lfBody struct {
	ID                  string          `json:"id"`
	TraceID             string          `json:"traceId"`
	Name                string          `json:"name"`
	StartTime           string          `json:"startTime"`
	EndTime             string          `json:"endTime"`
	CompletionStartTime string          `json:"completionStartTime"`
	Timestamp           string          `json:"timestamp"`
	ParentObservationID *string         `json:"parentObservationId"`
	Level               string          `json:"level"`
	StatusMessage       string          `json:"statusMessage"`
	Version             string          `json:"version"`
	Environment         string          `json:"environment"`
	Release             string          `json:"release"`
	UserID              *string         `json:"userId"`
	SessionID           *string         `json:"sessionId"`
	Metadata            json.RawMessage `json:"metadata"`
	Input               json.RawMessage `json:"input"`
	Output              json.RawMessage `json:"output"`
	Model               *string         `json:"model"`
	ModelParameters     json.RawMessage `json:"modelParameters"`
	UsageDetails        map[string]any  `json:"usageDetails"`
	// Usage is the primary SDK field; usageDetails is the newer spelling.
	// Accept both so official-SDK token counts are never dropped.
	Usage         map[string]any `json:"usage"`
	CostDetails   map[string]any `json:"costDetails"`
	Tags          []string       `json:"tags"`
	PromptName    *string        `json:"promptName"`
	PromptVersion *int           `json:"promptVersion"`
	Public        *bool          `json:"public"`
}

type envelope struct {
	Batch []ingestEvent `json:"batch"`
}

// ParseIngestion converts the JSON ingestion payload into span rows.
// Unknown body fields are dropped rather than rejected: SDKs evolve faster
// than we do, and a dropped field must never fail an ingestion.
func ParseIngestion(payload []byte, projectID string, prices map[string]store.Price) ([]store.Span, []store.Score, error) {
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, nil, fmt.Errorf("invalid ingestion payload: %w", err)
	}
	spans := make([]store.Span, 0, len(env.Batch))
	scores := make([]store.Score, 0, 4)
	now := time.Now().UnixMilli()

	for _, ev := range env.Batch {
		var body lfBody
		if len(ev.Body) > 0 {
			_ = json.Unmarshal(ev.Body, &body)
		}
		if body.ID == "" {
			body.ID = ev.ID
		}

		if ev.Type == "score-create" {
			s := store.Score{
				ProjectID: projectID,
				Name:      body.Name,
				DataType:  "NUMERIC",
				Source:    "API",
				CreatedAt: now,
			}
			var generic map[string]any
			_ = json.Unmarshal(ev.Body, &generic)
			if generic != nil {
				if v, ok := generic["value"].(float64); ok {
					s.Value = &v
				}
				if sv, ok := generic["stringValue"].(string); ok {
					s.StringValue = sv
				}
				if c, ok := generic["comment"].(string); ok {
					s.Comment = c
				}
				if dt, ok := generic["dataType"].(string); ok && dt != "" {
					s.DataType = dt
				}
				if tid, ok := generic["traceId"].(string); ok {
					s.TraceID = tid
				}
			}
			if s.Name != "" {
				scores = append(scores, s)
			}
			continue
		}

		typ := "span"
		switch ev.Type {
		case "trace-create":
			typ = "trace"
		case "generation-create", "generation-update":
			typ = "generation"
		case "event-create":
			typ = "event"
		case "sdk-log":
			continue
		}

		start, ok := parseTime(body.StartTime)
		if !ok {
			start, ok = parseTime(body.Timestamp)
		}
		if !ok {
			start, _ = parseTime(ev.Timestamp)
		}
		if start == 0 {
			start = now
		}
		eventTS, ok := parseTime(ev.Timestamp)
		if !ok {
			eventTS = start
		}

		traceID := body.TraceID
		if typ == "trace" {
			traceID = body.ID
		}
		if traceID == "" {
			traceID = body.ID
		}
		spanID := body.ID
		if spanID == "" {
			spanID = fmt.Sprintf("span_%d_%s", start, ev.Type)
		}

		row := store.Span{
			ProjectID:     projectID,
			SpanID:        spanID,
			TraceID:       traceID,
			ParentSpanID:  deref(body.ParentObservationID),
			Type:          typ,
			Name:          body.Name,
			StartTime:     start,
			SessionID:     deref(body.SessionID),
			UserID:        deref(body.UserID),
			Environment:   defaultStr(body.Environment, "default"),
			Release:       body.Release,
			Version:       body.Version,
			Tags:          tagsJSON(body.Tags),
			Level:         defaultStr(body.Level, "DEFAULT"),
			StatusMessage: body.StatusMessage,
			Metadata:      jsonText(body.Metadata),
			Input:         jsonText(body.Input),
			Output:        jsonText(body.Output),
			EventTS:       eventTS,
			CreatedAt:     now,
			UpdatedAt:     now,
		}

		if body.EndTime != "" {
			if v, ok := parseTime(body.EndTime); ok {
				row.EndTime = ptrInt(v)
			}
		}
		if body.CompletionStartTime != "" {
			if v, ok := parseTime(body.CompletionStartTime); ok {
				row.CompletionStartTime = ptrInt(v)
			}
		}
		if body.Model != nil {
			row.Model = *body.Model
		}
		row.ModelParameters = jsonText(body.ModelParameters)
		usage := mergeUsage(body.Usage, body.UsageDetails)
		row.UsageDetails = usageJSON(usage)
		row.CostDetails = costJSON(body.CostDetails)
		if row.CostDetails == "" {
			row.CostDetails = computeCost(row.Model, usage, prices)
		}
		if body.PromptName != nil {
			row.PromptName = *body.PromptName
		}
		if body.PromptVersion != nil {
			v := int64(*body.PromptVersion)
			row.PromptVersion = &v
		}

		spans = append(spans, row)
	}
	return spans, scores, nil
}
