package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// normalizeHistoryMessages trims history to the configured limit and assigns IDs.
func normalizeHistoryMessages(messages []Message, maxMessages int) []Message {
	if maxMessages > 0 && len(messages) > maxMessages {
		messages = messages[len(messages)-maxMessages:]
	}
	return assignMessageIDs(messages)
}

func assignMessageIDs(messages []Message) []Message {
	if len(messages) == 0 {
		return messages
	}
	result := make([]Message, len(messages))
	for index, message := range messages {
		result[index] = assignMessageID(message)
	}
	return result
}

func assignMessageID(message Message) Message {
	switch typedMessage := message.(type) {
	case TextMessage:
		if typedMessage.ID == "" {
			typedMessage.ID = generateMessageID()
		}
		return typedMessage
	case *TextMessage:
		if typedMessage == nil {
			return message
		}
		if typedMessage.ID == "" {
			typedMessage.ID = generateMessageID()
		}
		return typedMessage
	case ToolCallMessage:
		if typedMessage.ID == "" {
			typedMessage.ID = generateMessageID()
		}
		return typedMessage
	case *ToolCallMessage:
		if typedMessage == nil {
			return message
		}
		if typedMessage.ID == "" {
			typedMessage.ID = generateMessageID()
		}
		return typedMessage
	case ToolResultMessage:
		if typedMessage.ID == "" {
			typedMessage.ID = generateMessageID()
		}
		return typedMessage
	case *ToolResultMessage:
		if typedMessage == nil {
			return message
		}
		if typedMessage.ID == "" {
			typedMessage.ID = generateMessageID()
		}
		return typedMessage
	default:
		return message
	}
}

func generateMessageID() string {
	var randomBytes [8]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		return fmt.Sprintf("msg-%d", time.Now().UnixNano())
	}
	return "msg-" + hex.EncodeToString(randomBytes[:])
}
