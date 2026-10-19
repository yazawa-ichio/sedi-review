package diff

import (
	"fmt"
	"strings"
)

// unquote は git の C 風の引用（core.quotePath）を解く。引用されていなければそのまま返す。
func unquote(s string) (string, error) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s, nil
	}
	body := s[1 : len(s)-1]
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(body) {
			return "", fmt.Errorf("引用の終わりが不正です: %s", s)
		}
		switch e := body[i]; e {
		case 'a':
			b.WriteByte('\a')
		case 'b':
			b.WriteByte('\b')
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'v':
			b.WriteByte('\v')
		case 'f':
			b.WriteByte('\f')
		case 'r':
			b.WriteByte('\r')
		case '"', '\\':
			b.WriteByte(e)
		default:
			if e < '0' || e > '3' || i+2 >= len(body) {
				return "", fmt.Errorf("引用の書き方が不正です: %s", s)
			}
			v := 0
			for k := 0; k < 3; k++ {
				d := body[i+k]
				if d < '0' || d > '7' {
					return "", fmt.Errorf("引用の書き方が不正です: %s", s)
				}
				v = v*8 + int(d-'0')
			}
			b.WriteByte(byte(v))
			i += 2
		}
	}
	return b.String(), nil
}

// splitPathPair は "diff --git" の後ろの 2 つのパスを分ける。
func splitPathPair(s string) (string, string, error) {
	if strings.HasPrefix(s, `"`) {
		end := closingQuote(s)
		if end < 0 || end+1 >= len(s) || s[end+1] != ' ' {
			return "", "", fmt.Errorf("パスの組を読めません: %s", s)
		}
		return s[:end+1], s[end+2:], nil
	}
	if strings.HasSuffix(s, `"`) {
		start := strings.LastIndex(s, ` "`)
		if start < 0 {
			return "", "", fmt.Errorf("パスの組を読めません: %s", s)
		}
		return s[:start], s[start+1:], nil
	}
	if len(s)%2 == 1 {
		n := len(s) / 2
		if s[n] == ' ' {
			return s[:n], s[n+1:], nil
		}
	}
	i := strings.Index(s, " ")
	if i < 0 {
		return "", "", fmt.Errorf("パスの組を読めません: %s", s)
	}
	return s[:i], s[i+1:], nil
}

func closingQuote(s string) int {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}
