package zapret

import (
	"testing"
	"time"
)

func TestLastBenchmarkRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if lb, err := LoadLastBenchmark(dir); err != nil || lb != nil {
		t.Fatalf("без файла ожидается (nil, nil), получено (%v, %v)", lb, err)
	}
	st := AutoBenchmarkStatus{
		Method: "all", TargetGroup: "yt_discord", TotalStrategies: 22, Stopped: true,
		Leaderboard: []StrategyScore{
			{StrategyID: "general (ALT11)", StrategyName: "General [ALT11]", SuccessCount: 7, TotalCount: 8, AvgLatencyMs: 196},
			{StrategyID: "general", StrategyName: "General", SuccessCount: 3, TotalCount: 8, AvgLatencyMs: 142},
		},
	}
	at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	if err := SaveLastBenchmark(dir, LastBenchmarkFromStatus(st, at)); err != nil {
		t.Fatal(err)
	}
	lb, err := LoadLastBenchmark(dir)
	if err != nil || lb == nil {
		t.Fatalf("не удалось прочитать результат: %v", err)
	}
	if lb.StrategyID != "general (ALT11)" || lb.SuccessCount != 7 || lb.TotalCount != 8 || lb.Tested != 2 || lb.Total != 22 || !lb.Stopped || !lb.FinishedAt.Equal(at) {
		t.Fatalf("неверный результат: %+v", lb)
	}
}
