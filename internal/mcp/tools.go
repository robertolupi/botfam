package mcp

func tools() []map[string]any {
	return []map[string]any{
		tool("send", "Send a message to another actor.", map[string]any{
			"to": str(true), "type": str(true), "payload": obj(false), "in_reply_to": str(false), "expires_at": num(false), "actor": str(false),
		}),
		tool("recv", "Block until a message is reserved, or timeout.", map[string]any{
			"match_type": str(false), "timeout_s": num(false), "actor": str(false),
		}),
		tool("try_recv", "Reserve the oldest matching message if present.", map[string]any{
			"match_type": str(false), "actor": str(false),
		}),
		tool("peek", "Inspect the oldest matching message without reserving it.", map[string]any{
			"match_type": str(false), "actor": str(false),
		}),
		tool("ack", "Ack a reserved message.", map[string]any{
			"id": str(true), "outcome": obj(false), "actor": str(false),
		}),
		tool("seen", "Check whether a message id has been acked.", map[string]any{
			"id": str(true), "actor": str(false),
		}),
		tool("inbox", "Show mailbox and task counts.", map[string]any{"actor": str(false)}),
		tool("post", "Post a task.", map[string]any{
			"type": str(false), "payload": obj(false), "actor": str(false),
		}),
		tool("claim", "Claim one open task.", map[string]any{
			"lease_ttl": num(false), "actor": str(false),
		}),
		tool("complete", "Complete an owned task.", map[string]any{
			"task_id": str(true), "result": obj(false), "actor": str(false),
		}),
		tool("heartbeat", "Extend an owned task lease.", map[string]any{
			"task_id": str(true), "lease_ttl": num(false), "actor": str(false),
		}),
		tool("abandon", "Release an owned task back to open.", map[string]any{
			"task_id": str(true), "reason": str(false), "actor": str(false),
		}),
		tool("sweep", "Return expired claimed tasks to open.", map[string]any{"actor": str(false)}),
	}
}

func tool(name, desc string, props map[string]any) map[string]any {
	required := []string{}
	properties := map[string]any{}
	for k, v := range props {
		spec := v.(map[string]any)
		if req, ok := spec["_required"].(bool); ok && req {
			required = append(required, k)
		}
		delete(spec, "_required")
		properties[k] = spec
	}
	return map[string]any{
		"name":        name,
		"description": desc,
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": properties,
			"required":   required,
		},
	}
}

func str(required bool) map[string]any {
	return map[string]any{"type": "string", "_required": required}
}

func num(required bool) map[string]any {
	return map[string]any{"type": "number", "_required": required}
}

func obj(required bool) map[string]any {
	return map[string]any{"type": "object", "_required": required}
}
