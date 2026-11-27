package deal

import "bytes"

// gateElements finds every <script> and <style> element of a page, with its
// body. The emitter refuses inline code that holds its closing tag, so the
// first closing tag ends the element.
//
// It returns what
//
//	regexp.MustCompile(`(?is)<script\b[^>]*>(.*?)</script\s*>|<style\b[^>]*>(.*?)</style\s*>`).FindAllSubmatchIndex(page, -1)
//
// returns, byte for byte (TestGateElementsMatchTheExpression holds it to
// that): for each element, its start and end, then its body's start and end
// as a script's (group 1) or a style's (group 2), the other group -1. The
// expression read a whole page, runtime and all, through the regexp engine on
// every gate, most of a share's time under the race detector
// (evidencebook#1040); this reads it with byte searches.
//
// The expression's rules, as kept here: the names match case-insensitively,
// as Unicode simple folding does, so 's' is also U+017F (ſ); \b after a name
// is an ASCII word boundary; [^>]* runs to the first '>'; \s is
// [\t\n\f\r ]; matches are leftmost and do not overlap.
func gateElements(page []byte) [][]int {
	var out [][]int
	// noClose[k] is set once a search for kind k's closing tag from some
	// body start found none: every later body start is past it, so none
	// would find one either. noGT likewise for the end of a start tag.
	var noClose [2]bool
	noGT := false
	for at := 0; at < len(page); {
		i := bytes.IndexByte(page[at:], '<')
		if i < 0 {
			break
		}
		p := at + i
		m := gateElementAt(page, p, &noClose, &noGT)
		if m == nil {
			at = p + 1
			continue
		}
		out = append(out, m)
		at = m[1]
	}
	return out
}

// gateElementNames are the two kinds, in the expression's order.
var gateElementNames = [2]string{"script", "style"}

// gateElementAt is the element that starts at page[p] ('<'), or nil.
func gateElementAt(page []byte, p int, noClose *[2]bool, noGT *bool) []int {
	for k, name := range gateElementNames {
		n, ok := foldPrefix(page[p+1:], name)
		if !ok {
			continue
		}
		after := p + 1 + n
		if after < len(page) && asciiWordByte(page[after]) {
			continue // no \b
		}
		// Checked before the start tag's '>' is searched for, so a run of
		// unclosed start tags costs one search, not one each.
		if noClose[k] {
			continue
		}
		if *noGT {
			return nil
		}
		gt := bytes.IndexByte(page[after:], '>')
		if gt < 0 {
			*noGT = true
			return nil
		}
		body := after + gt + 1
		end, closeAt := gateClosing(page, body, name)
		if end < 0 {
			noClose[k] = true
			continue
		}
		m := []int{p, end, -1, -1, -1, -1}
		m[2+2*k], m[3+2*k] = body, closeAt
		return m
	}
	return nil
}

// gateClosing finds the first closing tag of name (</name\s*>) at or after
// from, and returns the end of the tag and where it starts, or -1, -1.
func gateClosing(page []byte, from int, name string) (int, int) {
	for at := from; at < len(page); {
		i := bytes.Index(page[at:], []byte("</"))
		if i < 0 {
			return -1, -1
		}
		c := at + i
		if n, ok := foldPrefix(page[c+2:], name); ok {
			j := c + 2 + n
			for j < len(page) && gateSpace(page[j]) {
				j++
			}
			if j < len(page) && page[j] == '>' {
				return j + 1, c
			}
		}
		at = c + 1
	}
	return -1, -1
}

// foldPrefix reports whether b starts with the ASCII lower-case word name
// under Unicode simple case folding, and how many bytes of b it spans: each
// letter matches either case, and 's' also matches U+017F (ſ, two bytes).
func foldPrefix(b []byte, name string) (int, bool) {
	n := 0
	for i := 0; i < len(name); i++ {
		if n >= len(b) {
			return 0, false
		}
		c := name[i]
		switch {
		case b[n]|0x20 == c:
			n++
		case c == 's' && b[n] == 0xC5 && n+1 < len(b) && b[n+1] == 0xBF:
			n += 2
		default:
			return 0, false
		}
	}
	return n, true
}

// asciiWordByte is \w for \b: [0-9A-Za-z_].
func asciiWordByte(c byte) bool {
	return c == '_' || '0' <= c && c <= '9' || 'a' <= c|0x20 && c|0x20 <= 'z'
}

// gateSpace is \s: [\t\n\f\r ].
func gateSpace(c byte) bool {
	return c == '\t' || c == '\n' || c == '\f' || c == '\r' || c == ' '
}
