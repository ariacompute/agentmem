package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"agentmem/internal/auth"
)

// domainError is a tool-level rejection with a fixed code and message. The
// message is asserted by the tool, never derived from an internal error, so no
// body, SQL, DSN or credential can leak through a rejection.
type domainError struct {
	code int
	msg  string
}

func (e *domainError) Error() string { return e.msg }

func invalid(msg string) error  { return &domainError{code: CodeInvalidParams, msg: msg} }
func notFound(msg string) error { return &domainError{code: CodeNotFound, msg: msg} }

// listTools returns the registered tools in declaration order. Mutating tools
// are absent unless Config.Write is set: visibility is the first gate, the
// per-tool scope check is the second.
func (s *Server) listTools() []map[string]any {
	out := make([]map[string]any, 0, len(s.ordered))
	for _, t := range s.ordered {
		if t.Mutating && !s.cfg.Write {
			continue
		}
		entry := map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.InputSchema,
		}
		if t.OutputSchema != nil {
			entry["outputSchema"] = t.OutputSchema
		}
		if t.Mutating {
			entry["annotations"] = map[string]any{
				"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false,
			}
		} else {
			entry["annotations"] = map[string]any{
				"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true,
			}
		}
		out = append(out, entry)
	}
	return out
}

func (s *Server) callTool(ctx context.Context, req Request, p auth.Principal) *Response {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &call); err != nil || strings.TrimSpace(call.Name) == "" {
		return errorResponse(req.ID, CodeInvalidParams, "invalid params")
	}
	tool, ok := s.tools[call.Name]
	if !ok || (tool.Mutating && !s.cfg.Write) {
		// A hidden write tool is indistinguishable from an unknown one, so the
		// disabled surface is never advertised.
		return errorResponse(req.ID, CodeInvalidParams, "unknown tool")
	}
	if !p.Allows(tool.Scope) {
		return errorResponse(req.ID, CodeForbidden, "forbidden")
	}
	args := call.Arguments
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	out, err := tool.run(ctx, p.Subject(), args)
	if err != nil {
		var te *ToolError
		if errors.As(err, &te) {
			return resultResponse(req.ID, toolResult(te.Structured, te.Message, true))
		}
		var de *domainError
		if errors.As(err, &de) {
			return errorResponse(req.ID, de.code, de.msg)
		}
		switch {
		case errors.Is(err, context.Canceled):
			return errorResponse(req.ID, CodeInternal, "request cancelled")
		default:
			return errorResponse(req.ID, CodeInternal, "internal error")
		}
	}
	return resultResponse(req.ID, toolResult(out, "", false))
}

// toolResult keeps the 2024-11-05 shape (a text content block) while adding the
// 2025-06-18 structuredContent object, so old and new clients both work.
func toolResult(structured any, text string, isError bool) map[string]any {
	if text == "" {
		if encoded, err := json.Marshal(structured); err == nil {
			text = string(encoded)
		}
	}
	result := map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
	if structured != nil {
		result["structuredContent"] = structured
	}
	return result
}

// Schema helpers. They are intentionally small: a tool schema is data, and the
// transport never interprets it.
func objectSchema(props map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringSchema(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func intSchema(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func boolSchema(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

func arraySchema(desc string, items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items, "description": desc}
}

func stringMapSchema(desc string) map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": map[string]any{"type": "string"},
		"description": desc,
	}
}

func objectItems() map[string]any { return map[string]any{"type": "object"} }

var (
	nodeListOutput    = objectSchema(map[string]any{"nodes": arraySchema("matching nodes", objectItems())})
	conceptOutput     = objectSchema(map[string]any{"concepts": arraySchema("concept ids", map[string]any{"type": "string"})})
	decisionOutput    = objectSchema(map[string]any{"decisions": arraySchema("routing decisions", objectItems())})
	computationOutput = objectSchema(map[string]any{"computations": arraySchema("computation verdicts", objectItems())})
)

func (s *Server) registerTools() {
	s.addSearchTools()
	s.addReadTools()
	s.addWriteTools()
}
