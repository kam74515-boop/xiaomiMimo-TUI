package model

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Channel classifies a model by its promotion stage.
type Channel string

const (
	ChannelDefault   Channel = "default"
	ChannelCandidate Channel = "candidate"
	ChannelLabs      Channel = "labs"

	DefaultMiMoBaseURL = "https://token-plan-cn.xiaomimimo.com/v1"
)

// Info describes a registered MiMo model variant.
type Info struct {
	ID           string
	BaseURL      string
	Channel      Channel
	ContextLimit int
	Description  string
	Accepted     bool // true after replay gate passed (candidate → default promotion)
}

// Registry is a threadsafe collection of known model endpoints.
type Registry struct {
	mu        sync.RWMutex
	models    map[string]Info
	defaultID string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{models: make(map[string]Info)}
}

// DefaultRegistry returns a pre-seeded registry with the standard MiMo models.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(Info{
		ID:           "mimo-v2.5-pro",
		BaseURL:      DefaultMiMoBaseURL,
		Channel:      ChannelDefault,
		ContextLimit: 1_000_000,
		Description:  "Primary MiMo model — 1M context, multi-step agent loop.",
		Accepted:     true,
	})
	r.Register(Info{
		ID:           "mimo-v2.5-flash",
		BaseURL:      DefaultMiMoBaseURL,
		Channel:      ChannelCandidate,
		ContextLimit: 128_000,
		Description:  "Fast MiMo candidate — 128K context, low-latency responses.",
		Accepted:     false,
	})
	r.Register(Info{
		ID:           "mimo-v2-pro",
		BaseURL:      DefaultMiMoBaseURL,
		Channel:      ChannelCandidate,
		ContextLimit: 256_000,
		Description:  "MiMo v2 candidate — 256K context, improved reasoning.",
		Accepted:     false,
	})
	r.defaultID = "mimo-v2.5-pro"
	return r
}

// Register adds a model to the registry. If an entry with the same ID already
// exists it is overwritten. If this is the first registered model it becomes
// the default automatically.
func (r *Registry) Register(info Info) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.models[info.ID] = info
	if r.defaultID == "" {
		r.defaultID = info.ID
	}
}

// Get returns the model info for id. The second return value is false when the
// id is not registered.
func (r *Registry) Get(id string) (Info, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.models[id]
	return info, ok
}

// Default returns the currently selected default model info.
func (r *Registry) Default() Info {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.models[r.defaultID]
}

// SetDefault promotes a registered model to be the default. The model must
// already be registered.
func (r *Registry) SetDefault(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.models[id]; !ok {
		return fmt.Errorf("model %q: %w", id, ErrNotRegistered)
	}
	r.defaultID = id
	return nil
}

// Candidates returns all models in the candidate channel.
func (r *Registry) Candidates() []Info {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Info
	for _, info := range r.models {
		if info.Channel == ChannelCandidate {
			out = append(out, info)
		}
	}
	return out
}

// AcceptCandidate promotes a candidate model to the default channel and sets
// it as the active default. Returns an error if the model is not in the
// candidate channel.
func (r *Registry) AcceptCandidate(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, ok := r.models[id]
	if !ok {
		return fmt.Errorf("model %q: %w", id, ErrNotRegistered)
	}
	if info.Channel != ChannelCandidate {
		return fmt.Errorf("model %q is not a candidate (channel=%s)", id, info.Channel)
	}
	info.Channel = ChannelDefault
	info.Accepted = true
	r.models[id] = info
	r.defaultID = id
	return nil
}

// Len returns the number of registered models.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.models)
}

// ListAll returns a snapshot of every registered model (thread-safe).
func (r *Registry) ListAll() []Info {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Info, 0, len(r.models))
	for _, info := range r.models {
		out = append(out, info)
	}
	return out
}

// DefaultID returns the currently selected default model ID.
func (r *Registry) DefaultID() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.defaultID
}

// ListModels returns a formatted table of registered models suitable for
// printing to stdout.
func (r *Registry) ListModels() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%-24s %-12s %12s %-10s %s\n", "ID", "CHANNEL", "CTX_LIMIT", "ACCEPTED", "DESCRIPTION"))
	for _, info := range r.models {
		accepted := "no"
		if info.Accepted {
			accepted = "yes"
		}
		ctxLimit := fmt.Sprintf("%d", info.ContextLimit)
		b.WriteString(fmt.Sprintf("%-24s %-12s %12s %-10s %s\n",
			info.ID, string(info.Channel), ctxLimit, accepted, info.Description))
	}
	return b.String()
}

// ErrNotRegistered is returned when a model lookup fails.
var ErrNotRegistered = errors.New("model not registered")
