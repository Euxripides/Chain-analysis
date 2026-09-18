FAILED ATTEMPT RECORD (preserved, never overwritten)
host_error: "Expected a semicolon" raised by the DSH code runtime while parsing the FIRST program that embedded
            the whole Go source inside a String.raw template literal: Go struct tags use the backtick character,
            which terminated the JS template literal -> host parse failure BEFORE any file write.
attempt_1_result: target file NOT created (no partial write). No retry of String.raw / backtick templates.
attempt_2_result: the same target was then created in full (5438 bytes) using a backtick-free construction.
                  That design was abandoned in favour of extending the EXISTING audited tool cmd/objectprobe
                  (additive action stat-strict) to avoid adding a new product-facing CLI.
frozen_file:      exportprobe-main.go.frozen  (byte copy of the abandoned attempt-2 file)
