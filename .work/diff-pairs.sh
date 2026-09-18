#!/usr/bin/env bash
cd "$(dirname "$0")/.." || exit 1
diffpair() { # <local> <remote-copy> <label>
  local l="$1" r=".work/remote/$2" label="$3"
  if diff -q "$l" "$r" >/dev/null 2>&1; then echo "== $label: 相同 =="; return; fi
  echo "== $label: 不同 (local $(wc -l < "$l") 行 / remote $(wc -l < "$r") 行) =="
  diff -u "$l" "$r" | head -n 40
  echo "   ...(diff 共 $(diff -u "$l" "$r" | wc -l) 行)"
}
diffpair backend/cmd/admin-fixture/main.go admin-fixture_main.go admin-fixture
diffpair backend/internal/api/mfa_second_step_live_test.go mfa_second_step_live_test.go mfa_second_step_live_test
diffpair backend/internal/iam/loginevent_guard_test.go loginevent_guard_test.go loginevent_guard_test
diffpair openapi/admin-v1.yaml admin-v1.yaml openapi/admin-v1
diffpair backend/internal/api/jsonstrings_test.go jsonstrings_test.go jsonstrings_test
