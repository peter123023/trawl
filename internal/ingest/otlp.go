package ingest

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/trawlhq/trawl/internal/store"
)

// attrNS is the wire prefix used by popular open-source LLM tracing SDKs.
const attrNS = "langfuse."

type otlpPayload struct {
	ResourceSpans []struct {
		Resource struct {
			Attributes []otlpAttr `json:"attributes"`
		} `json:"resource"`
		ScopeSpans []struct {
			Scope struct {
				Name string `json:"name"`
			} `json:"scope"`
			Spans []otlpSpan `json:"spans"`
		} `json:"scopeSpans"`
	} `json:"resourceSpans"`
}

type otlpAttr struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}

type otlpValue struct {
	Str    *string  `json:"stringValue"`
	Int    *string  `json:"intValue"`
	Double *float64 `json:"doubleValue"`
	Bool   *bool    `json:"boolValue"`
}

type otlpSpan struct {
	TraceID           string     `json:"traceId"`
	SpanID            string     `json:"spanId"`
	ParentSpanID      string     `json:"parentSpanId"`
	Name              string     `json:"name"`
	StartTimeUnixNano string     `json:"startTimeUnixNano"`
	EndTimeUnixNano   string     `json:"endTimeUnixNano"`
	Attributes        []otlpAttr `json:"attributes"`
	Status            struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"status"`
}

func attrString(a []otlpAttr) map[string]string {
	out := make(map[string]string, len(a))
	for _, kv := range a {
		switch {
		case kv.Value.Str != nil:
			out[kv.Key] = *kv.Value.Str
		case kv.Value.Int != nil:
			out[kv.Key] = *kv.Value.Int
		case kv.Value.Double != nil:
			out[kv.Key] = strconv.FormatFloat(*kv.Value.Double, 'f', -1, 64)
		case kv.Value.Bool != nil:
			if *kv.Value.Bool {
				out[kv.Key] = "true"
			} else {
				out[kv.Key] = "false"
			}
		}
	}
	return out
}

func nanoToMs(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n / 1e6, true
}

// ParseOTLP maps OTLP/HTTP JSON traces onto the span table. GenAI semantic
// conventions are read where present; everything else lands in metadata.
func ParseOTLP(payload []byte, projectID string, prices map[string]store.Price) ([]store.Span, error) {
	var p otlpPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	out := make([]store.Span, 0, 64)

	for _, rs := range p.ResourceSpans {
		resourceAttrs := attrString(rs.Resource.Attributes)
		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				attrs := attrString(sp.Attributes)
				start, ok := nanoToMs(sp.StartTimeUnixNano)
				if !ok {
					start = now
				}
				row := store.Span{
					ProjectID:    projectID,
					SpanID:       sp.SpanID,
					TraceID:      sp.TraceID,
					ParentSpanID: sp.ParentSpanID,
					Type:         "span",
					Name:         sp.Name,
					StartTime:    start,
					Environment:  defaultStr(attrs[attrNS+"environment"], "default"),
					SessionID:    attrs["session.id"],
					UserID:       attrs["user.id"],
					Level:        "DEFAULT",
					Tags:         tagsJSON(nil),
					EventTS:      start,
					CreatedAt:    now,
					UpdatedAt:    now,
				}

				if v, ok := nanoToMs(sp.EndTimeUnixNano); ok {
					row.EndTime = ptrInt(v)
				}
				if sp.Status.Code == 2 {
					row.Level = "ERROR"
					row.StatusMessage = sp.Status.Message
				}

				switch {
				case attrs[attrNS+"observation.type"] != "":
					row.Type = attrs[attrNS+"observation.type"]
				case attrs["gen_ai.operation.name"] != "":
					row.Type = attrs["gen_ai.operation.name"]
				case attrs["gen_ai.request.model"] != "":
					row.Type = "generation"
				}

				row.Model = attrs["gen_ai.request.model"]
				row.Version = attrs[attrNS+"version"]
				row.Release = attrs[attrNS+"release"]
				row.PromptName = attrs[attrNS+"prompt.name"]
				if row.PromptName == "" {
					row.PromptName = attrs[attrNS+"observation.promptName"]
				}
				if v := attrs[attrNS+"prompt.version"]; v != "" {
					if n, err := strconv.ParseInt(v, 10, 64); err == nil {
						row.PromptVersion = &n
					}
				}
				if v := attrs[attrNS+"session.id"]; v != "" && row.SessionID == "" {
					row.SessionID = v
				}
				if v := attrs[attrNS+"user.id"]; v != "" && row.UserID == "" {
					row.UserID = v
				}

				if v := attrs[attrNS+"observation.input"]; v != "" {
					row.Input = v
				} else if v := attrs["input.value"]; v != "" {
					row.Input = v
				}
				if v := attrs[attrNS+"observation.output"]; v != "" {
					row.Output = v
				} else if v := attrs["output.value"]; v != "" {
					row.Output = v
				}

				usage := map[string]any{}
				if v := attrs["gen_ai.usage.input_tokens"]; v != "" {
					if n, err := strconv.ParseFloat(v, 64); err == nil {
						usage["input"] = n
					}
				}
				if v := attrs["gen_ai.usage.output_tokens"]; v != "" {
					if n, err := strconv.ParseFloat(v, 64); err == nil {
						usage["output"] = n
					}
				}
				if len(usage) > 0 {
					row.UsageDetails = usageJSON(usage)
					row.CostDetails = computeCost(row.Model, usage, prices)
				}

				md := map[string]any{}
				for k, v := range attrs {
					md[k] = v
				}
				if ss.Scope.Name != "" {
					md["scope_name"] = ss.Scope.Name
				}
				for k, v := range resourceAttrs {
					md[k] = v
				}
				if b, err := json.Marshal(md); err == nil {
					row.Metadata = string(b)
				}

				out = append(out, row)
			}
		}
	}
	return out, nil
}
