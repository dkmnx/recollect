// terms.go — the shared text normalizer and tokenizer for fact search. One
// normalizer folds full-width text and the ideographic space, then
// lower-cases it, so a stored fact and a query agree on what a term is. One
// tokenizer splits text into terms: a run of letters and digits is one term,
// and a run of Han characters yields overlapping two-character terms,
// because Han text carries no word boundary.

package memory

import (
	"strings"
	"unicode"
)

// The full-width ASCII block, the fold that turns it into half-width ASCII,
// and the ideographic space. The block folds by a fixed offset.
const (
	fullWidthASCIIStart = 0xFF01
	fullWidthASCIIEnd   = 0xFF5E
	fullWidthASCIIFold  = 0xFEE0
	ideographicSpace    = 0x3000
)

// normalizeText folds full-width ASCII to half-width, folds the ideographic
// space to a space, and lower-cases the result. The width fold comes first,
// so the lower-case step works on half-width letters.
func normalizeText(text string) string {
	folded := strings.Map(func(r rune) rune {
		switch {
		case r >= fullWidthASCIIStart && r <= fullWidthASCIIEnd:
			return r - fullWidthASCIIFold
		case r == ideographicSpace:
			return ' '
		default:
			return r
		}
	}, text)
	return strings.ToLower(folded)
}

// tokenize splits text into terms. A run of letters and digits is one term.
// A run of Han characters yields overlapping two-character terms, because
// Han text has no word boundary to split on; a Han run of one character
// yields that character.
func tokenize(text string) []string {
	var terms []string
	var word []rune
	var han []rune
	flushWord := func() {
		if len(word) > 0 {
			terms = append(terms, string(word))
			word = word[:0]
		}
	}
	flushHan := func() {
		switch {
		case len(han) == 1:
			terms = append(terms, string(han))
		case len(han) > 1:
			for i := 0; i+1 < len(han); i++ {
				terms = append(terms, string(han[i:i+2]))
			}
		}
		han = han[:0]
	}
	for _, r := range normalizeText(text) {
		switch {
		case unicode.Is(unicode.Han, r):
			if len(word) > 0 {
				flushWord()
			}
			han = append(han, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if len(han) > 0 {
				flushHan()
			}
			word = append(word, r)
		default:
			flushWord()
			flushHan()
		}
	}
	flushWord()
	flushHan()
	return terms
}
