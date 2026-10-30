package macdaemon

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"zapretify/core/zapret"
)

// TestEngineDryRun asks a real utunws binary to check every bundled strategy after translation
// (--dry-run parses the options and loads lists and fakes, then exits). An @file replaces the whole
// command line, so --dry-run goes into the file itself.
// Opt-in: ZAPRETIFY_UTUNWS=/path/to/utunws go test ./core/macdaemon -run DryRun
func TestEngineDryRun(t *testing.T) {
	engine := os.Getenv("ZAPRETIFY_UTUNWS")
	if engine == "" {
		t.Skip("set ZAPRETIFY_UTUNWS to a utunws binary")
	}
	root, _ := filepath.Abs("../..")
	lists, bin := filepath.Join(root, "lists"), filepath.Join(root, "bin")
	data, err := os.ReadFile(filepath.Join(root, "strategies.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct{ Strategies []zapret.Strategy }
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for i, s := range file.Strategies {
		cfg, err := zapret.TranslateForUtunws(s.RawArgs, bin, lists)
		if err != nil {
			t.Errorf("%s: %v", s.ID, err)
			continue
		}
		conf := filepath.Join(dir, "s"+string(rune('a'+i))+".conf")
		if err := os.WriteFile(conf, []byte(zapret.WriteUtunwsConfig(append(cfg.Args, "--dry-run"))), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(engine, "@"+conf).CombinedOutput(); err != nil {
			t.Errorf("%s: %v\n%s", s.ID, err, out)
		}
	}
}
