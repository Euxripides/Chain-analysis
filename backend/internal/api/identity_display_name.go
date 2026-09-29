package api

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// normalizeIdentityDisplayName keeps account names readable in every console.
// React renders text safely, but markup-shaped names still break the header.
func normalizeIdentityDisplayName(raw string) (string, *fieldError) {
	name := strings.TrimSpace(raw)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return "", &fieldError{Field: "display_name", Code: "invalid_length", Message: "显示名称长度必须在 1-80 个字符之间"}
	}
	for _, r := range name {
		if r == '<' || r == '>' || unicode.IsControl(r) {
			return "", &fieldError{Field: "display_name", Code: "invalid_format", Message: "显示名称不能包含尖括号或控制字符"}
		}
	}
	return name, nil
}
