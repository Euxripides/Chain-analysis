package api

import (
	"strings"
	"testing"
)

func TestNormalizeIdentityDisplayName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		code  string
	}{
		{name: "Chinese name", input: "  管理员  ", want: "管理员"},
		{name: "ordinary name", input: "Alex Chen", want: "Alex Chen"},
		{name: "markup", input: `<img src=x onerror=alert(1)>`, code: "invalid_format"},
		{name: "line break", input: "admin\nowner", code: "invalid_format"},
		{name: "empty", input: "   ", code: "invalid_length"},
		{name: "too long", input: strings.Repeat("管", 81), code: "invalid_length"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, problem := normalizeIdentityDisplayName(tt.input)
			if got != tt.want {
				t.Fatalf("name = %q, want %q", got, tt.want)
			}
			if tt.code == "" && problem != nil {
				t.Fatalf("unexpected validation error: %+v", problem)
			}
			if tt.code != "" && (problem == nil || problem.Code != tt.code) {
				t.Fatalf("validation code = %v, want %q", problem, tt.code)
			}
		})
	}
}
