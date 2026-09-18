package exportmigrate

import (
	"errors"

	"github.com/jackc/pgx/v5"
)

// isNoRows 把 pgx 的"无行"判定集中一处，避免各实现重复 import pgx。
func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
