package store

import "strings"

// TailSplitSupersededError marks an audit-only parent shard that was replaced
// by child shards. It must not participate in progress denominators.
const TailSplitSupersededError = "split for tail acceleration"
const ForcedSubjectCompleteError = "forced address completion"

// EffectiveShardSQL returns the SQL predicate used for progress and completion
// calculations. alias is an internal, trusted table alias such as "sh".
func EffectiveShardSQL(alias string) string {
	prefix := ""
	if alias != "" {
		prefix = strings.TrimSuffix(alias, ".") + "."
	}
	return "NOT (" + prefix + "status='CANCELLED' AND " + prefix + "error IN ('" + TailSplitSupersededError + "','" + ForcedSubjectCompleteError + "'))"
}

func IsSupersededTailSplit(status, reason string) bool {
	return status == "CANCELLED" && reason == TailSplitSupersededError
}
