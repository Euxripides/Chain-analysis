package api

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestJobListSearchValidation(t *testing.T) {
	for _, test := range []struct {
		name, query, field string
	}{
		{"empty", "", ""},
		{"literal", "q=" + url.QueryEscape("  DL_%'  "), ""},
		{"nanosecond boundary", "created_from=2026-10-02T12:00:00.123456789%2B08:00&created_to=2026-10-02T04:00:00.123456789Z", ""},
		{"invalid start", "created_from=not-a-date", "created_from"},
		{"missing zone", "created_to=2026-10-02T12:00:00", "created_to"},
		{"reversed", "created_from=2026-10-03T00:00:00Z&created_to=2026-10-02T00:00:00Z", "created_to"},
		{"oversized", "q=" + url.QueryEscape(strings.Repeat("查", 201)), "q"},
	} {
		t.Run(test.name, func(t *testing.T) {
			filters, errors := parseJobListSearch(httptest.NewRequest("GET", "/api/v2/jobs?"+test.query, nil))
			if test.field == "" && len(errors) != 0 {
				t.Fatalf("unexpected errors: %+v", errors)
			}
			if test.field != "" && (len(errors) == 0 || errors[0].Field != test.field) {
				t.Fatalf("expected %s error, got %+v", test.field, errors)
			}
			if test.name == "literal" && filters.Query != "DL_%'" {
				t.Fatalf("literal query changed: %q", filters.Query)
			}
			if test.name == "nanosecond boundary" && (!filters.From.Equal(*filters.To) || filters.From.Nanosecond() != 123456789 || filters.From.Location() != time.UTC) {
				t.Fatalf("time precision or zone changed: %+v", filters)
			}
		})
	}
}
