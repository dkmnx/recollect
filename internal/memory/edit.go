package memory

import "strings"

// ApplyEdit replaces oldString with newString, literally: no regular
// expressions and no replacement-pattern expansion, so "$&" and friends
// land byte-for-byte. Zero matches and ambiguous matches (more than one
// without replaceAll) are coded errors.
func ApplyEdit(content, oldString, newString string, replaceAll bool) (string, int, error) {
	if oldString == "" {
		return "", 0, &Error{Code: CodeMissingField, Message: "old_string is required"}
	}
	count := strings.Count(content, oldString)
	switch {
	case count == 0:
		return "", 0, &Error{Code: CodeOldNotFound, Message: "old_string not found"}
	case count > 1 && !replaceAll:
		return "", 0, &Error{
			Code:    CodeOldAmbiguous,
			Message: "old_string is ambiguous; pass replaceAll or use a longer match",
		}
	}
	if replaceAll {
		return strings.ReplaceAll(content, oldString, newString), count, nil
	}
	return strings.Replace(content, oldString, newString, 1), 1, nil
}
