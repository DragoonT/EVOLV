// Package agent voice provides audible narration and speech capabilities for EVOLV.
// It allows the agent to speak status summaries, task completions, and dream insights,
// and coordinates voice events over the WebSocket bus.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dxn111/EVOLV/internal/bus"
)

// Utterance is an audible speech event emitted by the agent or API.
type Utterance struct {
	ID        string  `json:"id"`
	Text      string  `json:"text"`
	Voice     string  `json:"voice,omitempty"`
	Rate      float64 `json:"rate,omitempty"`
	Pitch     float64 `json:"pitch,omitempty"`
	Timestamp int64   `json:"timestamp"`
	Source    string  `json:"source,omitempty"`
}

// VoiceManager manages speech dispatch and utterance history.
type VoiceManager struct {
	mu      sync.RWMutex
	bus     *bus.Bus
	history []Utterance
	maxHist int
}

// NewVoiceManager creates a voice manager attached to the given bus.
func NewVoiceManager(b *bus.Bus) *VoiceManager {
	return &VoiceManager{
		bus:     b,
		history: make([]Utterance, 0, 50),
		maxHist: 50,
	}
}

// Speak broadcasts an utterance across the bus for client speech synthesis.
func (vm *VoiceManager) Speak(ctx context.Context, text, voice string, rate, pitch float64, source string) (*Utterance, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("empty speech text")
	}
	// Sanitize length to prevent excessive speech buffers
	if len(text) > 1000 {
		text = text[:1000] + "..."
	}

	if rate <= 0 {
		rate = 1.0
	}
	if pitch <= 0 {
		pitch = 1.0
	}
	if source == "" {
		source = "agent"
	}

	ut := Utterance{
		ID:        fmt.Sprintf("spk-%d", time.Now().UnixNano()),
		Text:      text,
		Voice:     voice,
		Rate:      rate,
		Pitch:     pitch,
		Timestamp: time.Now().UnixMilli(),
		Source:    source,
	}

	vm.mu.Lock()
	if len(vm.history) >= vm.maxHist {
		vm.history = vm.history[1:]
	}
	vm.history = append(vm.history, ut)
	vm.mu.Unlock()

	if vm.bus != nil {
		vm.bus.Publish("voice:speak", map[string]any{
			"id":        ut.ID,
			"text":      ut.Text,
			"voice":     ut.Voice,
			"rate":      ut.Rate,
			"pitch":     ut.Pitch,
			"timestamp": ut.Timestamp,
			"source":    ut.Source,
		})
	}

	return &ut, nil
}

// History returns a copy of recently dispatched utterances.
func (vm *VoiceManager) History() []Utterance {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	res := make([]Utterance, len(vm.history))
	copy(res, vm.history)
	return res
}

// Speak emits an audible voice statement through the voice manager and bus.
func (a *Agent) Speak(ctx context.Context, text, voice string, rate, pitch float64) (*Utterance, error) {
	if a == nil || a.voiceMgr == nil {
		return nil, errors.New("voice manager unavailable")
	}
	return a.voiceMgr.Speak(ctx, text, voice, rate, pitch, "agent")
}

// VoiceManager returns the agent's voice manager.
func (a *Agent) VoiceManager() *VoiceManager {
	if a == nil {
		return nil
	}
	return a.voiceMgr
}
