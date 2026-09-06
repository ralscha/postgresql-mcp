package sqlsafe

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var identifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var mutatingKeywords = map[string]struct{}{
	"ALTER": {}, "ANALYZE": {}, "BACKUP": {}, "BULK": {}, "CALL": {},
	"CLUSTER": {}, "COPY": {}, "CREATE": {}, "DELETE": {}, "DO": {},
	"DROP": {}, "EXEC": {}, "EXECUTE": {}, "GRANT": {}, "INSERT": {},
	"LISTEN": {}, "LOCK": {}, "MERGE": {}, "NOTIFY": {}, "REINDEX": {},
	"RESET": {}, "RESTORE": {}, "REVOKE": {}, "SET": {}, "TRUNCATE": {},
	"UPDATE": {}, "VACUUM": {},
}

func QuoteIdentifier(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !identifierRE.MatchString(name) {
		return "", fmt.Errorf("invalid identifier %q", name)
	}
	return `"` + name + `"`, nil
}

func QuoteMultipart(name string) (string, error) {
	parts := strings.Split(strings.TrimSpace(name), ".")
	if len(parts) == 0 || len(parts) > 2 {
		return "", fmt.Errorf("invalid multipart identifier %q", name)
	}
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		q, err := QuoteIdentifier(part)
		if err != nil {
			return "", err
		}
		quoted = append(quoted, q)
	}
	return strings.Join(quoted, "."), nil
}

// IsReadOnlyQuery accepts one SELECT statement (optionally introduced by WITH).
// PostgreSQL's read-only transaction mode remains the authoritative guard at
// execution time; this lexical check provides useful early errors and rejects
// multiple statements without mistaking strings or comments for SQL.
func IsReadOnlyQuery(query string) bool {
	tokens, _, ok := inspectStatement(query)
	if !ok || len(tokens) == 0 || (tokens[0] != "SELECT" && tokens[0] != "WITH") {
		return false
	}
	for _, token := range tokens[1:] {
		if _, mutating := mutatingKeywords[token]; mutating {
			return false
		}
	}
	return true
}

// AppendLimit wraps a query instead of trusting a caller-supplied LIMIT. This
// guarantees that the outermost result never contains more than maxRows rows.
func AppendLimit(query string, maxRows int) string {
	_, semicolon, _ := inspectStatement(query)
	q := strings.TrimSpace(query)
	if semicolon >= 0 {
		q = strings.TrimSpace(query[:semicolon])
	}
	return "SELECT * FROM (\n" + q + "\n) AS \"_postgresql_mcp_result\" LIMIT " + strconv.Itoa(maxRows)
}

// LikePattern treats '*' as the user-facing wildcard. PostgreSQL wildcard
// characters supplied by the user are escaped and therefore remain literals.
func LikePattern(s string) string {
	hasWildcard := strings.Contains(s, "*")
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	s = strings.ReplaceAll(s, `*`, `%`)
	if !hasWildcard {
		s = "%" + s + "%"
	}
	return s
}

// inspectStatement returns unquoted identifier-like tokens, the position of a
// trailing statement terminator, and whether the input is lexically complete.
// A semicolon is accepted only when comments and whitespace follow it.
func inspectStatement(query string) (tokens []string, semicolon int, ok bool) {
	semicolon = -1
	for i := 0; i < len(query); {
		if isSpace(query[i]) {
			i++
			continue
		}
		if i+1 < len(query) && query[i] == '-' && query[i+1] == '-' {
			i += 2
			for i < len(query) && query[i] != '\n' && query[i] != '\r' {
				i++
			}
			continue
		}
		if i+1 < len(query) && query[i] == '/' && query[i+1] == '*' {
			var complete bool
			i, complete = skipBlockComment(query, i)
			if !complete {
				return nil, -1, false
			}
			continue
		}
		if semicolon >= 0 {
			return nil, -1, false
		}

		switch query[i] {
		case ';':
			semicolon = i
			i++
			continue
		case '\'':
			var complete bool
			i, complete = skipQuoted(query, i, '\'')
			if !complete {
				return nil, -1, false
			}
			continue
		case '"':
			var complete bool
			i, complete = skipQuoted(query, i, '"')
			if !complete {
				return nil, -1, false
			}
			continue
		case '$':
			if delimiter, found := dollarQuoteDelimiter(query, i); found {
				end := strings.Index(query[i+len(delimiter):], delimiter)
				if end < 0 {
					return nil, -1, false
				}
				i += len(delimiter) + end + len(delimiter)
				continue
			}
		}

		if isIdentifierStart(query[i]) {
			start := i
			i++
			for i < len(query) && isIdentifierPart(query[i]) {
				i++
			}
			tokens = append(tokens, strings.ToUpper(query[start:i]))
			continue
		}
		i++
	}
	return tokens, semicolon, true
}

func skipQuoted(s string, start int, quote byte) (int, bool) {
	for i := start + 1; i < len(s); i++ {
		if s[i] == '\\' && quote == '\'' && i+1 < len(s) {
			i++
			continue
		}
		if s[i] != quote {
			continue
		}
		if i+1 < len(s) && s[i+1] == quote {
			i++
			continue
		}
		return i + 1, true
	}
	return len(s), false
}

func skipBlockComment(s string, start int) (int, bool) {
	depth := 1
	for i := start + 2; i < len(s); {
		switch {
		case i+1 < len(s) && s[i] == '/' && s[i+1] == '*':
			depth++
			i += 2
		case i+1 < len(s) && s[i] == '*' && s[i+1] == '/':
			depth--
			i += 2
			if depth == 0 {
				return i, true
			}
		default:
			i++
		}
	}
	return len(s), false
}

func dollarQuoteDelimiter(s string, start int) (string, bool) {
	if start+1 >= len(s) {
		return "", false
	}
	if s[start+1] == '$' {
		return "$$", true
	}
	if !isIdentifierStart(s[start+1]) {
		return "", false
	}
	for i := start + 2; i < len(s); i++ {
		if s[i] == '$' {
			return s[start : i+1], true
		}
		if !isIdentifierPart(s[i]) {
			return "", false
		}
	}
	return "", false
}

func isIdentifierStart(c byte) bool {
	return c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

func isIdentifierPart(c byte) bool {
	return isIdentifierStart(c) || c >= '0' && c <= '9'
}

func isSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\f':
		return true
	default:
		return false
	}
}
