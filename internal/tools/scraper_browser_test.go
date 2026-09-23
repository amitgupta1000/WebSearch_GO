package tools

import (
	"context"
	"testing"
	"time"
)

func TestSettleBudgetNeverOverrunsTheRemainingBudget(t *testing.T) {
	cases := []struct {
		name      string
		remaining time.Duration
		requested time.Duration
		want      time.Duration
	}{
		{"ample budget keeps the requested wait", 10 * time.Second, 4 * time.Second, 4 * time.Second},
		{"exact budget still reserves time to read the page", 4 * time.Second, 4 * time.Second, 4*time.Second - browserReadReserve},
		{"short budget caps the wait", 2 * time.Second, 4 * time.Second, 2*time.Second - browserReadReserve},
		{"only the reserve left skips the wait", browserReadReserve, 4 * time.Second, 0},
		{"inside the reserve skips the wait", browserReadReserve / 2, 4 * time.Second, 0},
		{"expired budget skips the wait", -time.Second, 4 * time.Second, 0},
		{"zero requested wait stays zero", 10 * time.Second, 0, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := settleBudget(tc.remaining, tc.requested); got != tc.want {
				t.Errorf("settleBudget(%v, %v) = %v, want %v", tc.remaining, tc.requested, got, tc.want)
			}
		})
	}
}

func TestRemainingMillisHandlesDeadlines(t *testing.T) {
	// No deadline: the requested wait is allowed in full.
	if got := remainingMillis(context.Background(), browserSettleTimeout); got != float64(browserSettleTimeout.Milliseconds()) {
		t.Errorf("a context without a deadline should allow the full wait, got %v", got)
	}

	// Expired deadline: skip the wait rather than overrun.
	expired, cancelExpired := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancelExpired()
	time.Sleep(time.Millisecond)
	if got := remainingMillis(expired, browserSettleTimeout); got != 0 {
		t.Errorf("an expired context must skip the wait, got %v", got)
	}

	// Generous deadline: still the requested wait.
	live, cancelLive := context.WithTimeout(context.Background(), time.Minute)
	defer cancelLive()
	if got := remainingMillis(live, browserSettleTimeout); got != float64(browserSettleTimeout.Milliseconds()) {
		t.Errorf("a generous deadline should allow the full wait, got %v", got)
	}

	// Tight deadline: capped to the remaining budget, never negative or over it.
	tight, cancelTight := context.WithTimeout(context.Background(), time.Second)
	defer cancelTight()
	got := remainingMillis(tight, browserNetworkIdleTimeout)
	if got <= 0 {
		t.Fatalf("a tight deadline should still allow a shortened wait, got %v", got)
	}
	if max := float64((time.Second - browserReadReserve).Milliseconds()); got > max {
		t.Errorf("capped wait %vms exceeds the available budget %vms", got, max)
	}
}
