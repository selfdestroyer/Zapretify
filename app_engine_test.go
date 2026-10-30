package main

import (
	"testing"

	"zapretify/core/config"
	"zapretify/core/tgproxy"
	"zapretify/core/zapret"
)

// fakeEngine stands in for a platform engine to check that App relies only on zapret.Engine.
type fakeEngine struct {
	running  bool
	strategy string
	stops    int
}

func (f *fakeEngine) Start(strategyID, _, _, _, _, _ string) error {
	f.running, f.strategy = true, strategyID
	return nil
}

func (f *fakeEngine) Stop() error {
	f.running, f.strategy = false, ""
	f.stops++
	return nil
}

func (f *fakeEngine) IsRunning() bool           { return f.running }
func (f *fakeEngine) GetActiveStrategy() string { return f.strategy }

func TestAppUsesEngineInterface(t *testing.T) {
	eng := &fakeEngine{running: true, strategy: "general_alt"}
	app := NewAppWith(config.Get().Clone(), eng, zapret.GetRegistry(), tgproxy.GetServer())

	st := app.snapshotZapretState()
	if !st.procRunning || st.strategy != "general_alt" {
		t.Fatalf("snapshot = %+v, want running general_alt", st)
	}

	if err := app.StopZapret(); err != nil {
		t.Fatalf("StopZapret: %v", err)
	}
	if eng.stops != 1 || eng.IsRunning() {
		t.Fatalf("engine not stopped: stops=%d running=%v", eng.stops, eng.IsRunning())
	}
}
