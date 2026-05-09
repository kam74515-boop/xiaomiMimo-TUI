package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessageMarshalMultimodalContentParts(t *testing.T) {
	msg := Message{
		Role: "user",
		ContentParts: []ContentPart{
			{Type: "image_url", ImageURL: &ImageURLPart{URL: "data:image/png;base64,abc"}},
			{Type: "text", Text: "describe this image"},
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"content":[`) {
		t.Fatalf("message did not encode array content: %s", text)
	}
	if !strings.Contains(text, `"image_url":{"url":"data:image/png;base64,abc"}`) {
		t.Fatalf("message missing image URL part: %s", text)
	}
	if !strings.Contains(text, `"text":"describe this image"`) {
		t.Fatalf("message missing text part: %s", text)
	}
}

func TestToolSpecMarshalNativeWebSearch(t *testing.T) {
	spec := ToolSpec{
		Type:        "web_search",
		MaxKeyword:  3,
		Limit:       2,
		ForceSearch: true,
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal tool spec: %v", err)
	}
	text := string(data)
	for _, want := range []string{`"type":"web_search"`, `"max_keyword":3`, `"limit":2`, `"force_search":true`} {
		if !strings.Contains(text, want) {
			t.Fatalf("tool spec %s missing %s", text, want)
		}
	}
	if strings.Contains(text, `"function"`) {
		t.Fatalf("native web_search spec must not include function field: %s", text)
	}
}

func TestToolSpecMarshalFunction(t *testing.T) {
	spec := ToolSpec{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        "read_file",
			Description: "read a file",
			Parameters:  JSONSchema{"type": "object"},
		},
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal tool spec: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"type":"function"`) || !strings.Contains(text, `"name":"read_file"`) {
		t.Fatalf("function tool spec encoded incorrectly: %s", text)
	}
}
