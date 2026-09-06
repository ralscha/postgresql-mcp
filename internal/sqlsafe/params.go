package sqlsafe

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// BindNamedParameters replaces PostgreSQL-style named placeholders such as
// $customer_id with positional placeholders. Repeated names reuse the same
// argument, and quoted text and comments are left untouched.
func BindNamedParameters(query string, params map[string]any, offset int) (string, []any, error) {
	if offset < 0 {
		return "", nil, fmt.Errorf("parameter offset cannot be negative")
	}

	var result strings.Builder
	result.Grow(len(query))
	positions := make(map[string]int, len(params))
	args := make([]any, 0, len(params))

	for i := 0; i < len(query); {
		if i+1 < len(query) && query[i] == '-' && query[i+1] == '-' {
			end := i + 2
			for end < len(query) && query[end] != '\n' && query[end] != '\r' {
				end++
			}
			result.WriteString(query[i:end])
			i = end
			continue
		}
		if i+1 < len(query) && query[i] == '/' && query[i+1] == '*' {
			end, complete := skipBlockComment(query, i)
			if !complete {
				return "", nil, fmt.Errorf("unterminated block comment")
			}
			result.WriteString(query[i:end])
			i = end
			continue
		}
		if query[i] == '\'' || query[i] == '"' {
			end, complete := skipQuoted(query, i, query[i])
			if !complete {
				return "", nil, fmt.Errorf("unterminated quoted value")
			}
			result.WriteString(query[i:end])
			i = end
			continue
		}
		if query[i] != '$' {
			result.WriteByte(query[i])
			i++
			continue
		}

		if delimiter, found := dollarQuoteDelimiter(query, i); found {
			closing := strings.Index(query[i+len(delimiter):], delimiter)
			if closing < 0 {
				return "", nil, fmt.Errorf("unterminated dollar-quoted value")
			}
			end := i + len(delimiter) + closing + len(delimiter)
			result.WriteString(query[i:end])
			i = end
			continue
		}
		if i+1 < len(query) && query[i+1] >= '0' && query[i+1] <= '9' {
			end := i + 2
			for end < len(query) && query[end] >= '0' && query[end] <= '9' {
				end++
			}
			return "", nil, fmt.Errorf("positional placeholder %q is not supported; use a named placeholder", query[i:end])
		}
		if i+1 >= len(query) || !isIdentifierStart(query[i+1]) {
			result.WriteByte(query[i])
			i++
			continue
		}

		end := i + 2
		for end < len(query) && isIdentifierPart(query[end]) {
			end++
		}
		name := query[i+1 : end]
		value, exists := params[name]
		if !exists {
			return "", nil, fmt.Errorf("missing value for named placeholder $%s", name)
		}
		position, seen := positions[name]
		if !seen {
			args = append(args, value)
			position = offset + len(args)
			positions[name] = position
		}
		result.WriteByte('$')
		result.WriteString(strconv.Itoa(position))
		i = end
	}

	unused := make([]string, 0, len(params)-len(positions))
	for name := range params {
		if _, used := positions[name]; !used {
			unused = append(unused, name)
		}
	}
	if len(unused) > 0 {
		sort.Strings(unused)
		return "", nil, fmt.Errorf("unused named parameters: %s", strings.Join(unused, ", "))
	}
	return result.String(), args, nil
}
