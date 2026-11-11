package translate

import "strings"

func validGlossary(value any) []any {
	result := []any{}
	for _, raw := range List(value) {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		en, enOK := entry["en"].(string)
		zh, zhOK := entry["zh"].(string)
		if enOK && zhOK && strings.TrimSpace(en) != "" && strings.TrimSpace(zh) != "" {
			result = append(result, entry)
		}
	}
	return result
}
