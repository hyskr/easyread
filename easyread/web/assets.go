// Package web embeds the same frontend used by the Python and desktop builds.
package web

import "embed"

// Files contains the complete offline frontend, including vendor assets.
//go:embed *.html *.svg *.ico css js i18n vendor
var Files embed.FS
