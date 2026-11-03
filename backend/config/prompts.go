package config

import (
	"fmt"
	"os"
	"strings"
)

const ChinesePrompt = `- 忠实：保留原文的论证顺序、章节编号、公式、表格、引用号 [n]、限定词（may/suggest/likely/at least）、否定和比较对象。可以调整中文语序、拆长句，读起来要像中文母语者写的学术文字。
- 只翻译，不解释、不总结、不加原文没有的内容。原文的笔误照录，不要改。
- 术语全文统一；首次出现的核心术语写“中文（English）”。已有术语表必须遵守。统计学里 standard error 译“标准误差”。`
const englishPrompt = `- Be faithful: preserve the argument order, section numbers, equations, tables, citations [n], qualifications (may/suggest/likely/at least), negation and objects of comparison. You may reorder clauses and split long sentences so the translation reads like academic writing by a native %s speaker.
- Translate only: do not explain, summarize or add information absent from the source. Preserve errors in the original without silently correcting them.
- Use terminology consistently throughout. On first occurrence, write core terms as %s. Follow the existing glossary.%s`
const traditionalPrompt = `- 忠實：保留原文的論證順序、章節編號、公式、表格、引用號 [n]、限定詞（may/suggest/likely/at least）、否定和比較對象。可以調整語序、拆長句，讀起來要像以%s為母語的人寫的學術文字。
- 只翻譯，不解釋、不總結、不加原文沒有的內容。原文的筆誤照錄，不要改。
- 術語全文統一；首次出現的核心術語寫「%s」。已有術語表必須遵守。%s`

func DefaultPrompt(target, language string) string {
	names := map[string]string{"zh": "Simplified Chinese", "zh-Hant": "Traditional Chinese", "en": "English", "ja": "Japanese", "ko": "Korean", "es": "Spanish", "fr": "French", "de": "German", "it": "Italian"}
	if names[target] == "" {
		target = "zh"
	}
	if language == "en" {
		term := `"` + names[target] + ` term (English)"`
		if target == "en" {
			term = `"English term (original-language term)"`
		}
		stats := ""
		if target == "zh" {
			stats = ` In statistics, translate standard error as "标准误差".`
		}
		if target == "zh-Hant" {
			stats = ` In statistics, translate standard error as "標準誤差".`
		}
		return fmt.Sprintf(englishPrompt, names[target], term, stats)
	}
	if language == "zh-Hant" {
		names := map[string]string{"zh": "簡體中文", "zh-Hant": "繁體中文", "en": "英文", "ja": "日文", "ko": "韓文", "es": "西班牙文", "fr": "法文", "de": "德文", "it": "義大利文"}
		term := names[target] + "譯名（English）"
		if target == "en" {
			term = "English term（原文術語）"
		}
		stats := ""
		if target == "zh" {
			stats = "統計學裡 standard error 譯為「标准误差」。"
		}
		if target == "zh-Hant" {
			stats = "統計學裡 standard error 譯為「標準誤差」。"
		}
		return fmt.Sprintf(traditionalPrompt, names[target], term, stats)
	}
	if target == "zh" {
		return ChinesePrompt
	}
	if target == "zh-Hant" {
		s := strings.ReplaceAll(ChinesePrompt, "中文母语者", "繁体中文使用者")
		s = strings.ReplaceAll(s, "中文（English）", "繁体中文（English）")
		return strings.ReplaceAll(s, "标准误差", "標準誤差")
	}
	namesCN := map[string]string{"en": "英文", "ja": "日语", "ko": "韩语", "es": "西班牙语", "fr": "法语", "de": "德语", "it": "意大利语"}
	s := strings.ReplaceAll(ChinesePrompt, "中文", namesCN[target])
	s = strings.ReplaceAll(s, "统计学里 standard error 译“标准误差”。", "")
	if target == "en" {
		s = strings.ReplaceAll(s, "英文（English）", "English term（原文术语）")
	}
	return s
}
func promptLocale() string {
	if v := os.Getenv("EASYREAD_LANG"); v == "zh" || v == "zh-Hant" || v == "en" {
		return v
	}
	for _, key := range []string{"EASYREAD_SYSTEM_LANG", "LC_ALL", "LC_MESSAGES", "LANG", "LANGUAGE"} {
		v := strings.ToLower(os.Getenv(key))
		if v == "" || v == "c" || v == "posix" || v == "c.utf-8" {
			continue
		}
		if strings.HasPrefix(v, "zh") {
			if strings.Contains(v, "hant") || strings.Contains(v, "tw") || strings.Contains(v, "hk") || strings.Contains(v, "mo") {
				return "zh-Hant"
			}
			return "zh"
		}
		return "en"
	}
	return "zh"
}
func EffectivePrompt(cfg Object, language string) string {
	factory := DefaultPrompt(text(cfg["target"]), language)
	if custom := strings.TrimSpace(text(cfg["translation_prompt"])); custom != "" {
		if custom == ChinesePrompt {
			return factory
		}
		return custom
	}
	if legacy := strings.TrimSpace(text(cfg["translation_instructions"])); legacy != "" {
		return factory + "\n\n" + legacy
	}
	return factory
}
