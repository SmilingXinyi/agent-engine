package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	agentprot "github.com/SmilingXinyi/agent-engine/agent/protocol"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	compressionprot "github.com/SmilingXinyi/agent-engine/compression/protocol"
	"github.com/SmilingXinyi/agent-engine/workspace/protocol"
)

const contextCheckpointVersion = 1
const contextCheckpointName = "context-checkpoint.json"

type contextCheckpoint struct {
	Version  int              `json:"version"`
	Sequence uint64           `json:"sequence"`
	RunID    string           `json:"run_id"`
	Messages []aiprot.Message `json:"messages"`
}

func (workspace *Workspace) loadContextCheckpoint() (*contextCheckpoint, error) {
	checkpoint, err := readContextCheckpoint(workspace.contextCheckpointPath())
	if err != nil {
		return nil, fmt.Errorf("read context checkpoint; restore it or reset the workspace: %w", err)
	}
	if checkpoint == nil || checkpoint.Sequence < workspace.history.lastReset {
		return nil, nil
	}
	run, exists := workspace.history.completedRuns[checkpoint.Sequence]
	if checkpoint.Version != contextCheckpointVersion || !exists || run.runID != checkpoint.RunID ||
		!slices.ContainsFunc(checkpoint.Messages, compressionprot.IsSummary) {
		return nil, errors.New("context checkpoint does not match the journal; restore it or reset the workspace")
	}
	request := agentprot.Request{Model: workspace.config.Model, History: checkpoint.Messages}
	if err := request.Validate(); err != nil {
		return nil, fmt.Errorf("invalid context checkpoint; restore it or reset the workspace: %w", err)
	}
	return checkpoint, nil
}

func readContextCheckpoint(path string) (*contextCheckpoint, error) {
	if err := regularOrMissing(path); err != nil {
		return nil, err
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var checkpoint contextCheckpoint
	if err := protocol.DecodeJSON(content, &checkpoint); err != nil {
		return nil, err
	}
	return &checkpoint, nil
}

func (workspace *Workspace) modelHistory() []aiprot.Message {
	checkpoint := workspace.checkpoint
	if checkpoint == nil {
		return workspace.history.snapshot()
	}
	run := workspace.history.completedRuns[checkpoint.Sequence]
	messages := make([]aiprot.Message, len(checkpoint.Messages))
	for index, message := range checkpoint.Messages {
		messages[index] = message.Clone()
	}
	return append(messages, workspace.history.snapshotAfter(run.messageCount)...)
}

func (workspace *Workspace) saveContextCheckpoint(runID string, messages []aiprot.Message) error {
	if !slices.ContainsFunc(messages, compressionprot.IsSummary) {
		return nil
	}
	// Agent prepends the current workspace system prompt. Rebuild it on
	// each run from the current workspace configuration.
	messages = messages[1:]
	checkpoint := &contextCheckpoint{
		Version: contextCheckpointVersion, Sequence: workspace.history.sequence,
		RunID: runID, Messages: make([]aiprot.Message, len(messages)),
	}
	for index, message := range messages {
		checkpoint.Messages[index] = message.Clone()
	}
	if err := writeContextCheckpoint(workspace.contextCheckpointPath(), checkpoint); err != nil {
		return fmt.Errorf("save context checkpoint: %w", err)
	}
	workspace.checkpoint = checkpoint
	return nil
}

func writeContextCheckpoint(path string, checkpoint *contextCheckpoint) error {
	if err := regularOrMissing(path); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, ".context-checkpoint-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	encodeErr := json.NewEncoder(file).Encode(checkpoint)
	if encodeErr == nil {
		encodeErr = file.Sync()
	}
	if err := errors.Join(encodeErr, file.Close()); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	return syncDirectory(directory)
}

func (workspace *Workspace) removeContextCheckpoint() error {
	path := workspace.contextCheckpointPath()
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err == nil {
		err = syncDirectory(filepath.Dir(path))
	}
	if err != nil {
		return fmt.Errorf("remove context checkpoint: %w", err)
	}
	return nil
}

func (workspace *Workspace) contextCheckpointPath() string {
	return filepath.Join(workspace.root, ".agents", "state", contextCheckpointName)
}
