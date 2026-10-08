package memory

import (
	"sync"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/model"
)

// ConversationMemory maintains chat message history.
type ConversationMemory interface {
	// Add appends messages to memory.
	Add(messages ...model.Message)
	// Messages returns all messages stored in memory.
	Messages() []model.Message
	// Clear clears stored messages.
	Clear()
}

// InactiveMemory does not keep any history.
type InactiveMemory struct{}

func (m *InactiveMemory) Add(messages ...model.Message) {}
func (m *InactiveMemory) Messages() []model.Message     { return nil }
func (m *InactiveMemory) Clear()                        {}

// BufferMemory stores messages in-memory with optional capacity limit.
type BufferMemory struct {
	mu       sync.RWMutex
	maxSize  int
	messages []model.Message
}

// NewBufferMemory creates a new BufferMemory.
func NewBufferMemory(maxSize int) *BufferMemory {
	return &BufferMemory{
		maxSize: maxSize,
	}
}

func (m *BufferMemory) Add(messages ...model.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.messages = append(m.messages, messages...)
	if m.maxSize > 0 && len(m.messages) > m.maxSize {
		m.messages = m.messages[len(m.messages)-m.maxSize:]
	}
}

func (m *BufferMemory) Messages() []model.Message {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make([]model.Message, len(m.messages))
	copy(res, m.messages)
	return res
}

func (m *BufferMemory) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = nil
}
