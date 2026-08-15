package executor

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/executor/docker_openhands/internal/platform"
)

type publishedLog struct {
	stream  string
	content string
}

// publishedLogsFromOpenHands extracts only text OpenHands explicitly publishes.
// Assistant messages are labelled reasoning in the operator UI; tool and
// observation output is work. Status and protocol metadata are ignored.
func publishedLogsFromOpenHands(value any) []publishedLog {
	logs := make([]publishedLog, 0)
	seen := make(map[string]struct{})
	appendLog := func(stream, content string) {
		content = strings.TrimSpace(content)
		if content == "" {
			return
		}
		fingerprint := stream + "\x00" + content
		if _, exists := seen[fingerprint]; exists {
			return
		}
		seen[fingerprint] = struct{}{}
		logs = append(logs, publishedLog{stream: stream, content: content})
	}
	collectPublishedLogs(value, "", appendLog)
	return logs
}

func collectPublishedLogs(value any, contextKind string, appendLog func(string, string)) {
	object, ok := value.(map[string]any)
	if !ok {
		if list, listOK := value.([]any); listOK {
			for _, item := range list {
				collectPublishedLogs(item, contextKind, appendLog)
			}
		}
		return
	}

	kind := strings.ToLower(firstString(object, "kind", "type", "event_type"))
	role := strings.ToLower(firstString(object, "role", "source"))
	if role == "assistant" || role == "agent" {
		appendTextContent(object["content"], "reasoning", appendLog)
		appendTextContent(object["message"], "reasoning", appendLog)
	}
	for _, key := range []string{"reasoning", "reasoning_content", "thought"} {
		appendTextContent(object[key], "reasoning", appendLog)
	}

	workContext := strings.Contains(kind, "observation") || strings.Contains(kind, "tool") ||
		strings.Contains(kind, "action") || strings.Contains(kind, "command") || strings.Contains(kind, "terminal") ||
		strings.Contains(contextKind, "observation") || strings.Contains(contextKind, "tool") || strings.Contains(contextKind, "action")
	if workContext {
		for _, key := range []string{"content", "action", "observation", "output", "stdout", "stderr", "message"} {
			appendTextContent(object[key], "work", appendLog)
		}
	}

	for key, nested := range object {
		switch key {
		case "content", "message", "reasoning", "reasoning_content", "thought", "action", "observation", "output", "stdout", "stderr":
			continue
		}
		collectPublishedLogs(nested, kind, appendLog)
	}
}

func appendTextContent(value any, stream string, appendLog func(string, string)) {
	switch typed := value.(type) {
	case string:
		appendLog(stream, typed)
	case []any:
		for _, part := range typed {
			appendTextContent(part, stream, appendLog)
		}
	case map[string]any:
		if text, ok := typed["text"].(string); ok {
			appendLog(stream, text)
		}
		for _, key := range []string{"content", "command", "output", "stdout", "stderr", "message", "summary"} {
			appendTextContent(typed[key], stream, appendLog)
		}
	}
}

func firstString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := object[key].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (e *Executor) appendPublishedOpenHandsLogs(ctx context.Context, taskID string, value any) {
	for _, item := range publishedLogsFromOpenHands(value) {
		if !e.claimPublishedLog(taskID, item) {
			continue
		}
		postCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err := e.v0002.AppendTaskLog(postCtx, taskID, platform.TaskLogAppendRequest{
			LogChunkID: "log-" + uuid.NewString(), Stream: item.stream,
			Content: item.content, OccurredAt: time.Now().UTC(),
		})
		cancel()
		if err != nil {
			e.logger.Warn("OpenHands log append failed", "task_id", taskID, "stream", item.stream, "err", err.Error())
		}
	}
}

func (e *Executor) claimPublishedLog(taskID string, item publishedLog) bool {
	e.logMu.Lock()
	defer e.logMu.Unlock()
	if len(e.publishedLog) >= 10_000 {
		clear(e.publishedLog)
	}
	key := taskID + "\x00" + item.stream + "\x00" + item.content
	if _, exists := e.publishedLog[key]; exists {
		return false
	}
	e.publishedLog[key] = struct{}{}
	return true
}
