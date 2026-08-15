package store

import (
	"testing"
	"time"
)

func TestUIDashboardWindow(t *testing.T) {
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		period      string
		key         string
		wantKey     string
		wantStart   time.Time
		wantBuckets int
		wantErr     bool
	}{
		{name: "current ISO week by default", period: "week", wantKey: "2026-W33", wantStart: time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC), wantBuckets: 7},
		{name: "ISO week crosses year boundary", period: "week", key: "2025-W01", wantKey: "2025-W01", wantStart: time.Date(2024, 12, 30, 0, 0, 0, 0, time.UTC), wantBuckets: 7},
		{name: "leap February", period: "month", key: "2024-02", wantKey: "2024-02", wantStart: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC), wantBuckets: 29},
		{name: "thirty day month", period: "month", key: "2026-04", wantKey: "2026-04", wantStart: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), wantBuckets: 30},
		{name: "future week rejected", period: "week", key: "2026-W34", wantErr: true},
		{name: "invalid ISO week rejected", period: "week", key: "2026-W54", wantErr: true},
		{name: "future month rejected", period: "month", key: "2026-09", wantErr: true},
		{name: "day is not a dashboard period", period: "day", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			window, err := uiDashboardWindow(test.period, test.key, now)
			if test.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("uiDashboardWindow: %v", err)
			}
			if window.key != test.wantKey || !window.start.Equal(test.wantStart) {
				t.Fatalf("window = key %q start %s", window.key, window.start)
			}
			buckets := window.buckets(now)
			if len(buckets) != test.wantBuckets {
				t.Fatalf("bucket count = %d, want %d", len(buckets), test.wantBuckets)
			}
			for index, bucket := range buckets {
				if bucket.Key != window.start.AddDate(0, 0, index).Format(time.DateOnly) || bucket.Total != 0 || len(bucket.Counts) != 5 {
					t.Fatalf("bucket %d = %#v", index, bucket)
				}
			}
		})
	}
}
