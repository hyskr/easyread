package translate

import "testing"

func TestInvalidGlossaryEntriesRemoved(t *testing.T) {
	entries := validGlossary([]any{Object{"en": "good", "zh": "有效"}, Object{"en": "missing"}, Object{"zh": "缺少"}, Object{"en": 42, "zh": "错误"}})
	if len(entries) != 1 {
		t.Fatal(entries)
	}
}
