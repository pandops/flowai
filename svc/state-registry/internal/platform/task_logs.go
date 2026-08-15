package platform

import "time"

const (
	TaskLogStreamWork      = "work"
	TaskLogStreamReasoning = "reasoning"
)

type TaskLogAppendRequest struct {
	LogChunkID string    `json:"log_chunk_id"`
	Stream     string    `json:"stream"`
	Content    string    `json:"content"`
	OccurredAt time.Time `json:"occurred_at"`
}

type TaskLogChunk struct {
	LogChunkID string    `json:"log_chunk_id"`
	TaskID     string    `json:"task_id"`
	TeamID     string    `json:"team_id"`
	ExecutorID string    `json:"executor_id"`
	LogOffset  int64     `json:"log_offset"`
	Stream     string    `json:"stream"`
	Content    string    `json:"content"`
	OccurredAt time.Time `json:"occurred_at"`
}

type TaskLogPage struct {
	Items      []TaskLogChunk `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}
