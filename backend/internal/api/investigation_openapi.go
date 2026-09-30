package api

import (
	"reflect"
	"strings"
)

func addInvestigationOpenAPI(paths map[string]any) {
	props := map[string]any{}
	typ := reflect.TypeOf(investigationRequest{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		name := f.Tag.Get("json")
		t := f.Type
		schema := map[string]any{}
		switch t.Kind() {
		case reflect.String:
			schema["type"] = "string"
		case reflect.Bool:
			schema["type"] = "boolean"
		case reflect.Int, reflect.Int64, reflect.Uint64:
			schema["type"] = "integer"
		case reflect.Slice:
			if name == "block" || name == "abi" {
				schema["description"] = "Block number/tag or JSON ABI as appropriate"
			} else {
				schema["type"] = "array"
				schema["items"] = map[string]any{}
			}
		case reflect.Map:
			schema["type"] = "object"
		}
		props[name] = schema
	}
	for _, op := range investigationOperations {
		prefix := "/api/v1/analysis/"
		if strings.Contains(op, "export") {
			prefix = "/api/v1/export/"
		}
		paths[prefix+op] = map[string]any{"post": map[string]any{"operationId": op, "summary": op, "description": "Authenticated investigation read. Monetary quantities remain strings. Current/historical EVM state, exact block provenance, bounded resources and evidence-qualified analysis; see MCP tool schema for operation-specific fields.", "security": []any{map[string]any{"bearerAuth": []any{}}}, "requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "additionalProperties": false, "properties": props}}}}, "responses": map[string]any{"200": actionResponse("Structured investigation result", "#/components/schemas/"+op+"Response"), "400": map[string]any{"description": "Invalid query"}, "401": map[string]any{"description": "Authentication required"}, "503": map[string]any{"description": "Provider/archive/budget unavailable; no inferred balance fallback"}}}}
	}
}

func addInvestigationSchemas(schemas map[string]any) {
	for _, op := range investigationOperations {
		props := map[string]any{}
		for _, key := range []string{"balance", "balance_raw", "balance_source", "total_amount", "seed_amount_raw", "sha256", "chunk_sha256", "content_base64", "resource_uri", "abi_source", "method", "input", "raw_output"} {
			props[key] = map[string]any{"type": []string{"string", "null"}}
		}
		for _, key := range []string{"as_of_block", "data_cutoff_block", "total_records", "total_groups", "size_bytes", "offset", "chunk_bytes"} {
			props[key] = map[string]any{"type": "integer"}
		}
		for _, key := range []string{"is_current_balance", "has_more", "complete", "discovery_complete", "verified", "full_integrity_verified"} {
			props[key] = map[string]any{"type": "boolean"}
		}
		for _, key := range []string{"items", "models", "evidence", "labels", "paths", "parameters", "output"} {
			props[key] = map[string]any{"type": "array", "items": map[string]any{}}
		}
		props["next_cursor"] = map[string]any{"type": []string{"string", "null"}}
		schemas[op+"Response"] = map[string]any{"type": "object", "required": []string{"success", "data", "meta", "error"}, "properties": map[string]any{"success": map[string]any{"type": "boolean"}, "data": map[string]any{"type": "object", "properties": props, "additionalProperties": true}, "meta": map[string]any{"$ref": "#/components/schemas/EvidenceMeta"}, "error": map[string]any{"type": []string{"object", "null"}}}}
	}
}
