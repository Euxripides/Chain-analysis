package app

import "testing"

func TestLocalActivityParticipants(t *testing.T) {
	tests := []struct {
		direction, wantFrom, wantTo string
		wantOK                      bool
	}{
		{direction: "in", wantFrom: "counterparty", wantTo: "subject", wantOK: true},
		{direction: "out", wantFrom: "subject", wantTo: "counterparty", wantOK: true},
		{direction: "self", wantFrom: "subject", wantTo: "subject", wantOK: true},
		{direction: "unknown", wantOK: false},
	}
	for _, test := range tests {
		from, to, ok := localActivityParticipants("subject", "counterparty", test.direction)
		if from != test.wantFrom || to != test.wantTo || ok != test.wantOK {
			t.Fatalf("direction=%s got=(%s,%s,%v) want=(%s,%s,%v)", test.direction, from, to, ok, test.wantFrom, test.wantTo, test.wantOK)
		}
	}
}

func TestLocalActivityTimestampMillis(t *testing.T) {
	for _, value := range []any{float64(1700000000123), "1700000000123"} {
		if got := localActivityTimestampMillis(value); got != 1700000000123 {
			t.Fatalf("value=%v timestamp=%d", value, got)
		}
	}
}
