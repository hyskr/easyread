package engines

import "strings"

// thinkFilter withholds split tag prefixes and never publishes unclosed thinking.
// Its pending buffer is bounded by the tag length, not by reasoning size.
type thinkFilter struct {
	thinking bool
	pending  string
}

func (f *thinkFilter) Push(piece string) string {
	f.pending += piece
	var out strings.Builder
	for f.pending != "" {
		tag := "<think>"
		if f.thinking {
			tag = "</think>"
		}
		if i := strings.Index(f.pending, tag); i >= 0 {
			if !f.thinking {
				out.WriteString(f.pending[:i])
			}
			f.pending = f.pending[i+len(tag):]
			f.thinking = !f.thinking
			continue
		}
		keep := 0
		for n := len(tag) - 1; n > 0; n-- {
			if strings.HasSuffix(f.pending, tag[:n]) {
				keep = n
				break
			}
		}
		visible := f.pending[:len(f.pending)-keep]
		if !f.thinking {
			out.WriteString(visible)
		}
		f.pending = f.pending[len(f.pending)-keep:]
		break
	}
	return out.String()
}
func (f *thinkFilter) Finish() string {
	p := f.pending
	f.pending = ""
	if f.thinking {
		return ""
	}
	return p
}
func stripThink(s string) string { f := thinkFilter{}; return f.Push(s) + f.Finish() }
