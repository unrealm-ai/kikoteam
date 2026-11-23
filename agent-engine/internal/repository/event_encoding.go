package repository

import "bytes"

// PostgreSQL cannot store NUL in text, including decoded JSONB strings. Rewrite
// the marshaled event only, not the tool data Pi received or its native history.
func replaceJSONNUL(data []byte) {
	for start := 0; start < len(data); {
		offset := bytes.IndexByte(data[start:], '\\')
		if offset < 0 {
			return
		}
		i := start + offset
		if i+6 <= len(data) && string(data[i:i+6]) == `\u0000` {
			copy(data[i:i+6], `\ufffd`)
			start = i + 6
		} else {
			// Skip the escaped byte so a literal "\\u0000" stays unchanged.
			start = i + 2
		}
	}
}
