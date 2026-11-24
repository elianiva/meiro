package mcpcontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
)

const (
	protocolVersion = "2025-11-25"
	instructions    = "Read now_playing before changing playback or the queue. Queue indexes are zero-based; use queue_list to check them before editing. Use video IDs from search or queue_list. Queue additions at end go before upcoming recommendations. Shuffle keeps the recommendation section in place. A collection_id from search replaces the queue in play_now. start_radio also replaces the queue. Check the rating in now_playing or queue_list before calling like. A rating of unknown means Meiro has no rating information. like requires sign-in. Duplicates are allowed. Previous restarts the current track after 3 seconds. Clear the upcoming queue only at the user's request; pass confirm: true. The current and played tracks stay. Read up to 50 queue items per page. Search returns up to 10 items, with 10 searches per minute and a 6-second gap. Next, previous, track play_now and start_radio share 20 calls per minute and a 2-second gap. Collection play_now allows 10 calls per minute with a 3-second gap. Ratings allow 10 calls per minute. Local commands share 120 calls per minute. Queue additions allow 1 to 10 IDs per call and 30 IDs per minute. If told to wait, wait that long and retry once."
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func encode(id json.RawMessage, result any, code int, message string) []byte {
	response := map[string]any{"jsonrpc": "2.0", "id": id}
	if code != 0 {
		response["error"] = map[string]any{"code": code, "message": message}
	} else {
		response["result"] = result
	}
	data, err := json.Marshal(response)
	if err != nil {
		return encode(id, nil, -32603, "Internal error")
	}
	return data
}

type session struct {
	version  string
	handler  Handler
	protocol string
}

func (s *session) dispatch(ctx context.Context, data []byte) []byte {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '[' && s.legacy() {
		var batch []json.RawMessage
		if json.Unmarshal(data, &batch) == nil && len(batch) > 0 {
			replies := make([]json.RawMessage, 0, len(batch))
			for _, entry := range batch {
				if reply := s.dispatchRequest(ctx, entry); len(reply) > 0 {
					replies = append(replies, reply)
				}
			}
			if len(replies) == 0 {
				return nil
			}
			result, err := json.Marshal(replies)
			if err != nil {
				return encode(nil, nil, -32603, "Internal error")
			}
			return result
		}
	}
	return s.dispatchRequest(ctx, data)
}

func (s *session) legacy() bool {
	return s.protocol == "2024-11-05" || s.protocol == "2025-03-26"
}

func (s *session) dispatchRequest(ctx context.Context, data []byte) []byte {
	if !json.Valid(data) {
		return encode(nil, nil, -32700, "Parse error")
	}
	var req request
	if json.Unmarshal(data, &req) != nil || req.JSONRPC != "2.0" || req.Method == "" {
		return encode(nil, nil, -32600, "Invalid request")
	}
	if len(req.ID) == 0 {
		return nil
	}
	switch req.ID[0] {
	case '"', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
	default:
		return encode(nil, nil, -32600, "Invalid request")
	}

	if s.protocol == "" && strings.HasPrefix(req.Method, "tools/") {
		return encode(req.ID, nil, -32002, "Server not initialized")
	}
	switch req.Method {
	case "initialize":
		if s.protocol != "" {
			return encode(req.ID, nil, -32600, "Server already initialized")
		}
		var params struct {
			Version string `json:"protocolVersion"`
		}
		if !decodeObject(req.Params, &params) || params.Version == "" {
			return encode(req.ID, nil, -32602, "Invalid params")
		}
		switch params.Version {
		case protocolVersion, "2025-06-18", "2025-03-26", "2024-11-05":
		default:
			params.Version = protocolVersion
		}
		s.protocol = params.Version
		version := s.version
		if version == "" {
			version = "development"
		}
		return encode(req.ID, map[string]any{
			"protocolVersion": params.Version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": "meiro", "version": version},
			"instructions":    instructions,
		}, 0, "")
	case "ping":
		return encode(req.ID, map[string]any{}, 0, "")
	case "tools/list":
		var params struct {
			Cursor string `json:"cursor"`
		}
		if len(req.Params) > 0 && !bytes.Equal(bytes.TrimSpace(req.Params), []byte("null")) && !decodeObject(req.Params, &params) {
			return encode(req.ID, nil, -32602, "Invalid params")
		}
		if params.Cursor != "" {
			return encode(req.ID, nil, -32602, "Invalid cursor")
		}
		if s.legacy() {
			definitions := make([]map[string]any, 0, len(tools))
			for _, tool := range tools {
				entry := map[string]any{"name": tool.Name, "description": tool.Description, "inputSchema": tool.Input}
				if s.protocol == "2025-03-26" {
					entry["annotations"] = tool.Annotations
				}
				definitions = append(definitions, entry)
			}
			return encode(req.ID, map[string]any{"tools": definitions}, 0, "")
		}
		return encode(req.ID, map[string]any{"tools": tools}, 0, "")
	case "tools/call":
		var params toolCall
		if !decodeObject(req.Params, &params) || params.Name == "" ||
			(len(params.Arguments) > 0 && !decodeObject(params.Arguments, &map[string]json.RawMessage{})) {
			return encode(req.ID, nil, -32602, "Invalid params")
		}
		if !KnownTool(params.Name) {
			return encode(req.ID, nil, -32602, "Unknown tool; use tools/list")
		}
		result, err := s.handler(ctx, params.Name, params.Arguments)
		if err != nil {
			return encode(req.ID, map[string]any{
				"isError": true, "content": []any{map[string]string{"type": "text", "text": err.Error()}},
			}, 0, "")
		}
		structured, err := json.Marshal(result)
		if err != nil {
			return encode(req.ID, nil, -32603, "Internal error")
		}
		response := map[string]any{"content": []any{map[string]string{"type": "text", "text": string(structured)}}}
		if !s.legacy() {
			response["structuredContent"] = json.RawMessage(structured)
		}
		return encode(req.ID, response, 0, "")
	default:
		return encode(req.ID, nil, -32601, "Method not found")
	}
}

func decodeObject(data json.RawMessage, into any) bool {
	data = bytes.TrimSpace(data)
	return len(data) > 0 && data[0] == '{' && json.Unmarshal(data, into) == nil
}
