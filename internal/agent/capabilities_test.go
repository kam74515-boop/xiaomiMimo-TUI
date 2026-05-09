package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mimo-tui/internal/core"
)

func TestBuildUserMessageAttachesLocalImage(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "screen.png")
	if err := os.WriteFile(imagePath, []byte("png-bytes"), 0o600); err != nil {
		t.Fatalf("write image fixture: %v", err)
	}

	msg := BuildUserMessage("帮我看看图片 " + imagePath)
	if msg.Role != "user" || msg.Content == "" {
		t.Fatalf("message basics = %#v", msg)
	}
	if len(msg.ContentParts) != 2 {
		t.Fatalf("content parts = %#v, want image + text", msg.ContentParts)
	}
	if msg.ContentParts[0].Type != "image_url" || msg.ContentParts[0].ImageURL == nil {
		t.Fatalf("first part = %#v, want image_url", msg.ContentParts[0])
	}
	if !strings.HasPrefix(msg.ContentParts[0].ImageURL.URL, "data:image/png;base64,") {
		t.Fatalf("image URL = %q, want PNG data URI", msg.ContentParts[0].ImageURL.URL)
	}
	if msg.ContentParts[1].Type != "text" || !strings.Contains(msg.ContentParts[1].Text, "帮我看看图片") {
		t.Fatalf("second part = %#v, want original text", msg.ContentParts[1])
	}
}

func TestBuildUserMessageAttachesMediaURL(t *testing.T) {
	msg := BuildUserMessage("分析这个视频 https://example.com/demo.mp4")
	if len(msg.ContentParts) != 2 {
		t.Fatalf("content parts = %#v, want video + text", msg.ContentParts)
	}
	part := msg.ContentParts[0]
	if part.Type != "video_url" || part.VideoURL == nil || part.VideoURL.URL != "https://example.com/demo.mp4" {
		t.Fatalf("video part = %#v", part)
	}
}

func TestAugmentToolSpecsAddsNativeWebSearchByDefault(t *testing.T) {
	t.Setenv("MIMO_WEB_SEARCH", "")
	specs := augmentToolSpecs([]core.ToolSpec{
		{Type: "function", Function: core.ToolFunctionSpec{Name: "read_file"}},
	})
	if len(specs) != 2 {
		t.Fatalf("specs = %#v, want existing function + web_search", specs)
	}
	web := specs[1]
	if web.Type != "web_search" || web.MaxKeyword != 3 || web.Limit != 3 || web.ForceSearch {
		t.Fatalf("web spec = %#v", web)
	}
}

func TestAugmentToolSpecsCanDisableNativeWebSearch(t *testing.T) {
	t.Setenv("MIMO_WEB_SEARCH", "0")
	specs := augmentToolSpecs(nil)
	if len(specs) != 0 {
		t.Fatalf("specs = %#v, want no web_search when disabled", specs)
	}
}

func TestAugmentToolSpecsCanForceNativeWebSearch(t *testing.T) {
	t.Setenv("MIMO_WEB_SEARCH", "force")
	t.Setenv("MIMO_WEB_SEARCH_LIMIT", "7")
	specs := augmentToolSpecs(nil)
	if len(specs) != 1 {
		t.Fatalf("specs = %#v, want web_search", specs)
	}
	if !specs[0].ForceSearch || specs[0].Limit != 7 {
		t.Fatalf("web spec = %#v, want forced limit 7", specs[0])
	}
}

func TestNativeWebSearchMarshalInChatRequest(t *testing.T) {
	t.Setenv("MIMO_WEB_SEARCH", "")
	req := core.ChatRequest{
		Messages: []core.Message{BuildUserMessage("今天有什么新闻")},
		Tools:    augmentToolSpecs(nil),
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"type":"web_search"`) {
		t.Fatalf("request missing native web_search tool: %s", text)
	}
	if strings.Contains(text, `"function":{}`) {
		t.Fatalf("web_search request leaked empty function field: %s", text)
	}
}
