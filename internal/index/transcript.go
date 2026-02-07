package index

import "strings"

func FilterMessages(messages []Message, toggles TranscriptToggles) []Message {
	canonicalUsers := map[string]struct{}{}
	for _, m := range messages {
		if m.Type == "message" && m.Role == "user" {
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
