package zapret

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// LastBenchmarkFile хранит итог последнего подбора стратегии рядом с приложением.
// Интерфейс показывает его как «Прошлый подбор» и подсказку «Лучшая по подбору».
const LastBenchmarkFile = "last_benchmark.json"

type LastBenchmark struct {
	StrategyID   string    `json:"strategy_id"`
	StrategyName string    `json:"strategy_name"`
	SuccessCount int       `json:"success_count"`
	TotalCount   int       `json:"total_count"`
	AvgLatencyMs float64   `json:"avg_latency_ms"` // -1, если ни одна цель не открылась
	Method       string    `json:"method"`
	TargetGroup  string    `json:"target_group"`
	Tested       int       `json:"tested"` // сколько стратегий успели проверить
	Total        int       `json:"total"`  // сколько стратегий было в подборе
	Stopped      bool      `json:"stopped"`
	FinishedAt   time.Time `json:"finished_at"`
}

// LastBenchmarkFromStatus берёт победителя из итогового (отсортированного) статуса подбора.
func LastBenchmarkFromStatus(st AutoBenchmarkStatus, at time.Time) LastBenchmark {
	lb := LastBenchmark{
		Method:      st.Method,
		TargetGroup: st.TargetGroup,
		Tested:      len(st.Leaderboard),
		Total:       st.TotalStrategies,
		Stopped:     st.Stopped,
		FinishedAt:  at,
	}
	if len(st.Leaderboard) > 0 {
		best := st.Leaderboard[0]
		lb.StrategyID = best.StrategyID
		lb.StrategyName = best.StrategyName
		lb.SuccessCount = best.SuccessCount
		lb.TotalCount = best.TotalCount
		lb.AvgLatencyMs = best.AvgLatencyMs
	}
	return lb
}

// SaveLastBenchmark атомарно записывает результат: временный файл, затем переименование.
func SaveLastBenchmark(appDir string, lb LastBenchmark) error {
	data, err := json.MarshalIndent(lb, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(appDir, LastBenchmarkFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// LoadLastBenchmark возвращает (nil, nil), если подбор ещё не проводился.
func LoadLastBenchmark(appDir string) (*LastBenchmark, error) {
	data, err := os.ReadFile(filepath.Join(appDir, LastBenchmarkFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var lb LastBenchmark
	if err := json.Unmarshal(data, &lb); err != nil {
		return nil, err
	}
	if lb.StrategyID == "" {
		return nil, nil
	}
	return &lb, nil
}
