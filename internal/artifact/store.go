package artifact

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Store struct {
	root string
}

type Payload struct {
	Name string
	Data []byte
}

type WriteRequest struct {
	Tool     string
	Kind     string
	ExitCode int
	Inputs   map[string]any
	Payloads []Payload
}

type FileMetadata struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type Metadata struct {
	ID        string         `json:"id"`
	Tool      string         `json:"tool"`
	Kind      string         `json:"kind"`
	CreatedAt time.Time      `json:"created_at"`
	ExitCode  int            `json:"exit_code"`
	Inputs    map[string]any `json:"inputs,omitempty"`
	Files     []FileMetadata `json:"files"`
}

type Record struct {
	ID       string
	Dir      string
	Metadata Metadata
}

type PayloadRecord struct {
	Metadata FileMetadata
	Data     []byte
}

func NewStore(workspace string) *Store {
	if workspace == "" {
		workspace = "."
	}
	return &Store{root: filepath.Join(workspace, ".mimo", "artifacts")}
}

func (s *Store) Root() string {
	return s.root
}

func (s *Store) Write(req WriteRequest) (Record, error) {
	if s == nil {
		return Record{}, fmt.Errorf("artifact store is nil")
	}
	if req.Tool == "" {
		return Record{}, fmt.Errorf("artifact tool is required")
	}
	if req.Kind == "" {
		req.Kind = "tool_result"
	}

	id := newID()
	dir := filepath.Join(s.root, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Record{}, err
	}

	meta := Metadata{
		ID:        id,
		Tool:      req.Tool,
		Kind:      req.Kind,
		CreatedAt: time.Now().UTC(),
		ExitCode:  req.ExitCode,
		Inputs:    req.Inputs,
	}

	used := map[string]int{}
	for _, payload := range req.Payloads {
		name := cleanName(payload.Name)
		used[name]++
		if used[name] > 1 {
			ext := filepath.Ext(name)
			base := strings.TrimSuffix(name, ext)
			name = fmt.Sprintf("%s-%d%s", base, used[name], ext)
		}

		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, payload.Data, 0o600); err != nil {
			return Record{}, err
		}
		sum := sha256.Sum256(payload.Data)
		meta.Files = append(meta.Files, FileMetadata{
			Name:   strings.TrimSuffix(name, filepath.Ext(name)),
			Path:   filepath.Join(id, name),
			Bytes:  len(payload.Data),
			SHA256: hex.EncodeToString(sum[:]),
		})
	}

	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return Record{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), raw, 0o600); err != nil {
		return Record{}, err
	}

	return Record{ID: id, Dir: dir, Metadata: meta}, nil
}

func (s *Store) ReadMetadata(id string) (Metadata, error) {
	if s == nil {
		return Metadata{}, fmt.Errorf("artifact store is nil")
	}
	if id == "" || strings.Contains(id, string(filepath.Separator)) {
		return Metadata{}, fmt.Errorf("invalid artifact id")
	}
	raw, err := os.ReadFile(filepath.Join(s.root, id, "metadata.json"))
	if err != nil {
		return Metadata{}, err
	}
	var meta Metadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return Metadata{}, err
	}
	return meta, nil
}

func (s *Store) ReadPayloads(id string) ([]PayloadRecord, error) {
	meta, err := s.ReadMetadata(id)
	if err != nil {
		return nil, err
	}

	records := make([]PayloadRecord, 0, len(meta.Files))
	for _, file := range meta.Files {
		path, err := s.payloadPath(id, file)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		records = append(records, PayloadRecord{Metadata: file, Data: data})
	}
	return records, nil
}

func (s *Store) payloadPath(id string, file FileMetadata) (string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(file.Path))
	if filepath.IsAbs(cleaned) || cleaned == "." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid artifact payload path")
	}
	prefix := filepath.Clean(id) + string(filepath.Separator)
	if !strings.HasPrefix(cleaned, prefix) {
		return "", fmt.Errorf("artifact payload path escapes artifact")
	}
	return filepath.Join(s.root, cleaned), nil
}

func newID() string {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Sprintf("art-%s", time.Now().UTC().Format("20060102T150405.000000000Z"))
	}
	return fmt.Sprintf("art-%s-%s", time.Now().UTC().Format("20060102T150405.000000000Z"), hex.EncodeToString(suffix[:]))
}

func cleanName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "content.txt"
	}
	return name
}
