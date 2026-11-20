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
	"unicode/utf8"
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
	text = normalizeText(text)
	var terms []string
	wordStart, hanStart, lastHan := -1, -1, -1
	for offset, r := range text {
		if unicode.Is(unicode.Han, r) {
			if wordStart >= 0 {
				terms = append(terms, text[wordStart:offset])
				wordStart = -1
			}
			if lastHan >= 0 {
				terms = append(terms, text[lastHan:offset+utf8.RuneLen(r)])
			} else {
				hanStart = offset
			}
			lastHan = offset
			continue
		}
		if lastHan >= 0 {
			if lastHan == hanStart {
				terms = append(terms, text[hanStart:offset])
			}
			lastHan = -1
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if wordStart < 0 {
				wordStart = offset
			}
		} else if wordStart >= 0 {
			terms = append(terms, text[wordStart:offset])
			wordStart = -1
		}
	}
	if wordStart >= 0 {
		terms = append(terms, text[wordStart:])
	}
	if lastHan >= 0 && lastHan == hanStart {
		terms = append(terms, text[hanStart:])
	}
	return terms
}
