package tron

import (
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

func TestReceiptControllerRaisesAndHalvesWithinBounds(t *testing.T) {
	c := newReceiptController(6, 12, 16, true)
	c.lastAdjust = time.Now().Add(-time.Minute)
	for i := 0; i < 20; i++ {
		c.observe(500*time.Millisecond, 200, "", false)
	}
	if c.target != 6 {
		t.Fatalf("target without queue=%d want 6", c.target)
	}
	c.waiting = 1
	c.lastAdjust = time.Now().Add(-time.Minute)
	for i := 0; i < 20; i++ {
		c.observe(500*time.Millisecond, 200, "", false)
	}
	if c.target != 8 {
		t.Fatalf("stable target=%d want 8", c.target)
	}
	c.lastAdjust = time.Now().Add(-time.Minute)
	c.observe(500*time.Millisecond, 429, provider.ClassRateLimit, false)
	if c.target != 4 {
		t.Fatalf("degraded target=%d want 4", c.target)
	}
}

func TestReceiptControllerDoesNotAdaptPastConfiguredMaximum(t *testing.T) {
	c := newReceiptController(6, 12, 16, true)
	c.waiting = 1
	for want := 8; want <= 12; want += 2 {
		c.lastAdjust = time.Now().Add(-time.Minute)
		for i := 0; i < 20; i++ {
			c.observe(500*time.Millisecond, 200, "", false)
		}
		if c.target != want {
			t.Fatalf("target=%d want %d", c.target, want)
		}
	}
	c.lastAdjust = time.Now().Add(-time.Minute)
	for i := 0; i < 20; i++ {
		c.observe(500*time.Millisecond, 200, "", false)
	}
	if c.target != 12 {
		t.Fatalf("target exceeded configured maximum: %d", c.target)
	}
}
