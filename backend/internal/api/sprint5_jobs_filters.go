package api

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type jobListSearch struct {
	Query string
	From  *time.Time
	To    *time.Time
}

func parseJobListSearch(r *http.Request) (jobListSearch, []fieldError) {
	query := r.URL.Query()
	filters := jobListSearch{Query: strings.TrimSpace(query.Get("q"))}
	errors := []fieldError{}
	if utf8.RuneCountInString(filters.Query) > 200 {
		errors = append(errors, fieldError{Field: "q", Code: "too_long", Message: "搜索词最多 200 个字符"})
	}
	for _, field := range []struct {
		name string
		out  **time.Time
	}{{"created_from", &filters.From}, {"created_to", &filters.To}} {
		value := strings.TrimSpace(query.Get(field.name))
		if value == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			errors = append(errors, fieldError{Field: field.name, Code: "invalid", Message: "创建时间须为包含时区的有效时间"})
			continue
		}
		utc := parsed.UTC()
		*field.out = &utc
	}
	if filters.From != nil && filters.To != nil && filters.From.After(*filters.To) {
		errors = append(errors, fieldError{Field: "created_to", Code: "invalid_range", Message: "结束时间不能早于开始时间"})
	}
	return filters, errors
}
