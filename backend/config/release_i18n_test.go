package config

import (
	"encoding/json"
	web "github.com/hyskr/easyread/web"
	"io"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Mirrors tests/app/test_release_i18n.py against the actual embedded release assets.
func TestReleaseEnglishDictionaryHasNoDuplicateKeys(t *testing.T) {
	data, err := web.Files.ReadFile("i18n/en.json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		t.Fatal("dictionary must be an object", token, err)
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			t.Fatal(err)
		}
		key, ok := token.(string)
		if !ok {
			t.Fatal(token)
		}
		if seen[key] {
			t.Errorf("duplicate English translation key: %q", key)
		}
		seen[key] = true
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		t.Fatal(token, err)
	}
	if _, err = decoder.Token(); err != io.EOF {
		t.Fatal("unexpected trailing JSON", err)
	}
}
func TestReleaseFeatureTranslationsPreserveNamedParameters(t *testing.T) {
	data, err := web.Files.ReadFile("i18n/en.json")
	if err != nil {
		t.Fatal(err)
	}
	en := map[string]string{}
	if err = json.Unmarshal(data, &en); err != nil {
		t.Fatal(err)
	}
	sources := []string{"common/page-cap.js", "library/settings-cloud.js", "library/cite-export.js", "common/cite.js", "common/library-nav.js", "library/import.js", "library/detail.js"}
	keys := regexp.MustCompile(`PR\.t\("([^"\n]+)"`)
	parameters := regexp.MustCompile(`\{[\pL\pN_]+\}`)
	chinese := regexp.MustCompile(`[一-鿿]`)
	set := func(s string) map[string]bool {
		result := map[string]bool{}
		for _, p := range parameters.FindAllString(s, -1) {
			result[p] = true
		}
		return result
	}
	checked := 0
	for _, source := range sources {
		code, err := web.Files.ReadFile("js/" + source)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range keys.FindAllStringSubmatch(string(code), -1) {
			key := match[1]
			checked++
			t.Run(source+"/"+key, func(t *testing.T) {
				translated := en[key]
				if translated == "" {
					t.Fatalf("missing English translation for %q", key)
				}
				if !reflect.DeepEqual(set(key), set(translated)) {
					t.Errorf("named parameters differ: %q => %q", key, translated)
				}
				if chinese.MatchString(translated) {
					t.Errorf("English translation contains Chinese characters: %q", translated)
				}
			})
		}
	}
	if checked == 0 {
		t.Fatal("no feature translation calls inspected")
	}
}
