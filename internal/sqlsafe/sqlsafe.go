package sqlsafe

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	identifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	mutatingRE   = regexp.MustCompile(`(?is)\b(INSERT|UPDATE|DELETE|MERGE|CREATE|ALTER|DROP|TRUNCATE|EXEC|EXECUTE|GRANT|REVOKE|COPY|VACUUM|ANALYZE|REINDEX|CLUSTER|CALL|DO|SET|RESET|LISTEN|NOTIFY|LOCK|BACKUP|RESTORE|BULK)\b`)
	limitRE      = regexp.MustCompile(`(?is)\bLIMIT\s+\d+`)
	fetchRE      = regexp.MustCompile(`(?is)\bFETCH\s+(FIRST|NEXT)\s+\d+\s+ROWS?\s+ONLY\b`)
)

func QuoteIdentifier(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !identifierRE.MatchString(name) {
		return "", fmt.Errorf("invalid identifier %q", name)
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`, nil
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

func IsReadOnlyQuery(query string) bool {
	q := strings.TrimSpace(stripLeadingComments(query))
	if q == "" || mutatingRE.MatchString(q) {
		return false
	}
	// Reject semicolons (multi-statement) except as trailing character
	if strings.Count(q, ";") > 1 || (strings.Count(q, ";") == 1 && !strings.HasSuffix(q, ";")) {
		return false
	}
	upper := strings.ToUpper(q)
	return strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "WITH")
}

func NeedsRowLimit(query string) bool {
	q := strings.TrimSpace(query)
	return !limitRE.MatchString(q) && !fetchRE.MatchString(q)
}

func AppendLimit(query string, maxRows int) string {
	q := strings.TrimSuffix(strings.TrimSpace(query), ";")
	return q + " LIMIT " + strconv.Itoa(maxRows)
}

func LikePattern(s string) string {
	hasWildcard := strings.Contains(s, "%") || strings.Contains(s, "*")
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	s = strings.ReplaceAll(s, `*`, `%`)
	if !hasWildcard {
		s = "%" + s + "%"
	}
	return s
}

func stripLeadingComments(q string) string {
	for {
		q = strings.TrimSpace(q)
		if strings.HasPrefix(q, "--") {
			if idx := strings.Index(q, "\n"); idx >= 0 {
				q = q[idx+1:]
				continue
			}
			return ""
		}
		if strings.HasPrefix(q, "/*") {
			if idx := strings.Index(q, "*/"); idx >= 0 {
				q = q[idx+2:]
				continue
			}
			return ""
		}
		return q
	}
}
