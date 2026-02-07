package index

import "strings"

func FilterMessages(messages []Message, toggles TranscriptToggles) []Message {
	canonicalUsers := map[string]struct{}{}
	for _, m := range messages {
		if m.Type == "message" && m.Role == "user" {
			if isBoilerplateUserContent(m.Content) {
				continue
			}
			n := normalizeContent(m.Content)
			if n != "" {
				canonicalUsers[n] = struct{}{}
			}
		}
	}

	filtered := make([]Message, 0, len(messages))
	for _, m := range messages {
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		if isBoilerplateUserMessage(m) {
			continue
		}

		if m.Type == "message" && (m.Role == "user" || m.Role == "assistant") {
			filtered = append(filtered, m)
			continue
		}

		if m.Type == "user_message" {
			if !toggles.IncludeAborted {
				continue
			}
			n := normalizeContent(m.Content)
			if _, exists := canonicalUsers[n]; exists {
				continue
			}
			filtered = append(filtered, m)
			continue
		}

		if isToolMessage(m) {
			if toggles.IncludeTools {
				filtered = append(filtered, m)
			}
			continue
		}

		if toggles.IncludeEvents {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

func isToolMessage(m Message) bool {
	if strings.Contains(strings.ToLower(m.Role), "tool") {
		return true
	}
	return strings.Contains(strings.ToLower(m.Type), "tool")
}

func normalizeContent(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.Join(strings.Fields(s), " ")
	return s
}

func isBoilerplateUserMessage(m Message) bool {
	if strings.ToLower(strings.TrimSpace(m.Role)) != "user" {
		return false
	}
	return isBoilerplateUserContent(m.Content)
}

func isBoilerplateUserContent(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)

	if strings.HasPrefix(lower, "<turn_aborted>") && strings.Contains(lower, "</turn_aborted>") {
		return true
	}
	if strings.HasPrefix(lower, "<environment_context>") {
		return true
	}
	if strings.Contains(lower, "<environment_context>") && strings.Contains(lower, "<cwd>") {
		return true
	}
	return false
}

func isNonConversationalPreviewContent(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return true
	}
	if isBoilerplateUserContent(trimmed) {
		return true
	}
	return strings.HasPrefix(strings.ToLower(trimmed), "# agents.md instructions for ")
}
