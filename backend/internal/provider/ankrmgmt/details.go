package ankrmgmt

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var usageRowPattern = regexp.MustCompile(`^- (\S+)\s+(.+):\s+([0-9][0-9,]*) calls,\s+([0-9][0-9,]*) credits$`)
var usageTotalPattern = regexp.MustCompile(`^Total:\s+([0-9][0-9,]*) calls,\s+([0-9][0-9,]*) credits\.?$`)
var keyCountPattern = regexp.MustCompile(`^(\d+) dedicated API key\(s\):$`)
var keyRowPattern = regexp.MustCompile(`^- index (\d+): (.*?) \[encrypted: (true|false); config: (.*)\]$`)

func parseUsageDetails(raw map[string]any, fromMs, toMs int64) (map[string]any, error) {
	text, ok := raw["text"].(string)
	if !ok {
		return nil, errors.New("Ankr usage response has no text")
	}
	rows := make([]map[string]any, 0)
	var totalRequests int64
	var totalCredits string
	foundTotal := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if match := usageRowPattern.FindStringSubmatch(line); match != nil {
			calls, err := strconv.ParseInt(strings.ReplaceAll(match[3], ",", ""), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("Ankr usage row count is invalid: %w", err)
			}
			credits := strings.ReplaceAll(match[4], ",", "")
			rows = append(rows, map[string]any{"network": match[1], "method": match[2],
				"request_count": calls, "credits": credits})
			continue
		}
		if match := usageTotalPattern.FindStringSubmatch(line); match != nil {
			var err error
			totalRequests, err = strconv.ParseInt(strings.ReplaceAll(match[1], ",", ""), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("Ankr total usage count is invalid: %w", err)
			}
			totalCredits = strings.ReplaceAll(match[2], ",", "")
			foundTotal = true
		}
	}
	if !foundTotal {
		return nil, errors.New("Ankr usage response has no total")
	}
	if totalRequests > 0 && len(rows) == 0 {
		return nil, errors.New("Ankr usage response has no call breakdown")
	}
	return map[string]any{"range": "last_24h", "from_ms": fromMs, "to_ms": toMs,
		"unit": "credits", "total_requests": totalRequests, "total_credits": totalCredits,
		"data": rows}, nil
}

func parseKeyDetails(raw map[string]any) (map[string]any, error) {
	text, ok := raw["text"].(string)
	if !ok {
		return nil, errors.New("Ankr key list response has no text")
	}
	rows := make([]map[string]any, 0)
	count := -1
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if match := keyCountPattern.FindStringSubmatch(line); match != nil {
			var err error
			count, err = strconv.Atoi(match[1])
			if err != nil {
				return nil, fmt.Errorf("Ankr key count is invalid: %w", err)
			}
			continue
		}
		if match := keyRowPattern.FindStringSubmatch(line); match != nil {
			index, err := strconv.Atoi(match[1])
			if err != nil {
				return nil, fmt.Errorf("Ankr key index is invalid: %w", err)
			}
			name := match[2]
			if name == "(none)" {
				name = ""
			}
			rows = append(rows, map[string]any{"index": index, "name": name,
				"encrypted": match[3] == "true", "config": match[4]})
		}
	}
	if count < 0 || count != len(rows) {
		return nil, errors.New("Ankr key list response is incomplete")
	}
	return map[string]any{"count": count, "data": rows}, nil
}
