package pdf

import "bytes"

// PDF text can contain glyph codes that Poppler writes literally into XML.
// Replace XML 1.0 forbidden characters before parsing the extracted content.
func cleanPDFXML(data []byte) []byte {
	return bytes.Map(func(r rune) rune {
		if r == 0x9 || r == 0xA || r == 0xD || r >= 0x20 && r <= 0xD7FF || r >= 0xE000 && r <= 0xFFFD || r >= 0x10000 && r <= 0x10FFFF {
			return r
		}
		return '\uFFFD'
	}, data)
}
