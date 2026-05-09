package agent

import (
	"encoding/base64"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"mimo-tui/internal/core"
)

const defaultMediaMaxBytes = 50 * 1024 * 1024

var (
	urlPattern    = regexp.MustCompile(`https?://[^\s\]\)\}"'<>]+`)
	quotedPattern = regexp.MustCompile(`["'“”‘’]([^"'“”‘’]+)["'“”‘’]`)

	imageMIMEs = map[string]string{
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".webp": "image/webp",
		".gif":  "image/gif",
		".bmp":  "image/bmp",
		".tif":  "image/tiff",
		".tiff": "image/tiff",
	}
	audioMIMEs = map[string]string{
		".wav":  "audio/wav",
		".mp3":  "audio/mpeg",
		".m4a":  "audio/mp4",
		".aac":  "audio/aac",
		".flac": "audio/flac",
		".ogg":  "audio/ogg",
		".opus": "audio/ogg",
	}
	videoMIMEs = map[string]string{
		".mp4":  "video/mp4",
		".mov":  "video/quicktime",
		".webm": "video/webm",
		".mkv":  "video/x-matroska",
		".avi":  "video/x-msvideo",
	}
)

// BuildUserMessage keeps the visible text prompt intact while attaching any
// referenced image/audio/video paths or URLs as MiMo multimodal content parts.
func BuildUserMessage(prompt string) core.Message {
	msg := core.Message{Role: "user", Content: prompt}
	mediaParts := mediaContentParts(prompt)
	if len(mediaParts) == 0 {
		return msg
	}
	parts := make([]core.ContentPart, 0, len(mediaParts)+1)
	parts = append(parts, mediaParts...)
	if strings.TrimSpace(prompt) != "" {
		parts = append(parts, core.ContentPart{Type: "text", Text: prompt})
	}
	msg.ContentParts = parts
	return msg
}

func augmentToolSpecs(specs []core.ToolSpec) []core.ToolSpec {
	out := append([]core.ToolSpec{}, specs...)
	if !nativeWebSearchEnabled() || hasToolSpec(out, "web_search") {
		return out
	}
	out = append(out, core.ToolSpec{
		Type:        "web_search",
		MaxKeyword:  envInt("MIMO_WEB_SEARCH_MAX_KEYWORD", 3),
		Limit:       envInt("MIMO_WEB_SEARCH_LIMIT", 3),
		ForceSearch: nativeWebSearchForced(),
	})
	return out
}

func nativeWebSearchEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MIMO_WEB_SEARCH"))) {
	case "0", "false", "off", "no", "disabled":
		return false
	default:
		return true
	}
}

func nativeWebSearchForced() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MIMO_WEB_SEARCH"))) {
	case "force", "forced", "always":
		return true
	default:
		return false
	}
}

func hasToolSpec(specs []core.ToolSpec, typ string) bool {
	for _, spec := range specs {
		if spec.Type == typ {
			return true
		}
	}
	return false
}

func mediaContentParts(prompt string) []core.ContentPart {
	candidates := mediaCandidates(prompt)
	if len(candidates) == 0 {
		return nil
	}
	seen := map[string]bool{}
	parts := make([]core.ContentPart, 0, len(candidates))
	for _, candidate := range candidates {
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		part, ok := mediaContentPart(candidate)
		if ok {
			parts = append(parts, part)
		}
	}
	return parts
}

func mediaCandidates(prompt string) []string {
	var out []string
	out = append(out, urlPattern.FindAllString(prompt, -1)...)
	for _, match := range quotedPattern.FindAllStringSubmatch(prompt, -1) {
		if len(match) > 1 {
			out = append(out, match[1])
		}
	}
	for _, field := range strings.Fields(prompt) {
		out = append(out, field)
	}
	for i := range out {
		out[i] = cleanMediaCandidate(out[i])
	}
	return out
}

func cleanMediaCandidate(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "@")
	value = strings.Trim(value, "`\"'“”‘’")
	value = strings.TrimRight(value, ".,;:!?，。；：！？)]}）】")
	value = strings.TrimLeft(value, "([{（【")
	return value
}

func mediaContentPart(ref string) (core.ContentPart, bool) {
	if ref == "" {
		return core.ContentPart{}, false
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		kind, ok := mediaKindForExt(extFromURL(ref))
		if !ok {
			return core.ContentPart{}, false
		}
		return partForMedia(kind, ref), true
	}

	localPath := expandLocalPath(ref)
	if localPath == "" {
		return core.ContentPart{}, false
	}
	info, err := os.Stat(localPath)
	if err != nil || info.IsDir() || info.Size() > mediaMaxBytes() {
		return core.ContentPart{}, false
	}
	kind, ok := mediaKindForExt(strings.ToLower(filepath.Ext(localPath)))
	if !ok {
		return core.ContentPart{}, false
	}
	data, err := os.ReadFile(localPath)
	if err != nil {
		return core.ContentPart{}, false
	}
	mimeType := mimeForFile(localPath, kind)
	dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(data))
	return partForMedia(kind, dataURI), true
}

func expandLocalPath(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		ref = filepath.Join(home, strings.TrimPrefix(ref, "~/"))
	}
	if filepath.IsAbs(ref) {
		return filepath.Clean(ref)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return filepath.Clean(filepath.Join(cwd, ref))
}

func extFromURL(ref string) string {
	parsed, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	return strings.ToLower(path.Ext(parsed.Path))
}

func mediaKindForExt(ext string) (string, bool) {
	if _, ok := imageMIMEs[ext]; ok {
		return "image", true
	}
	if _, ok := audioMIMEs[ext]; ok {
		return "audio", true
	}
	if _, ok := videoMIMEs[ext]; ok {
		return "video", true
	}
	return "", false
}

func partForMedia(kind string, value string) core.ContentPart {
	switch kind {
	case "image":
		return core.ContentPart{Type: "image_url", ImageURL: &core.ImageURLPart{URL: value}}
	case "audio":
		return core.ContentPart{Type: "input_audio", InputAudio: &core.InputAudioPart{Data: value}}
	case "video":
		return core.ContentPart{Type: "video_url", VideoURL: &core.VideoURLPart{URL: value}}
	default:
		return core.ContentPart{}
	}
}

func mimeForFile(file string, kind string) string {
	ext := strings.ToLower(filepath.Ext(file))
	if value := mime.TypeByExtension(ext); value != "" {
		return value
	}
	switch kind {
	case "image":
		return imageMIMEs[ext]
	case "audio":
		return audioMIMEs[ext]
	case "video":
		return videoMIMEs[ext]
	default:
		return "application/octet-stream"
	}
}

func mediaMaxBytes() int64 {
	mb := envInt("MIMO_MEDIA_MAX_MB", defaultMediaMaxBytes/(1024*1024))
	if mb <= 0 {
		mb = defaultMediaMaxBytes / (1024 * 1024)
	}
	return int64(mb) * 1024 * 1024
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
