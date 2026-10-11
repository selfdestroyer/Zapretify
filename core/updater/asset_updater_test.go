package updater

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseBatRawArgs(t *testing.T) {
	sampleBat := `@echo off
cd /d %BIN%
start "zapret: %~n0" /min "%BIN%winws.exe" --wf-tcp=80,443 --filter-tcp=443 ^
--hostlist="%LISTS%list-google.txt" --ip-id=zero ^
--dpi-desync=fake,multidisorder --dpi-desync-fake-tls=^! --new ^
--filter-udp=443 --dpi-desync=fake
`
	args := ParseBatRawArgs(sampleBat)
	expected := `--wf-tcp=80,443 --filter-tcp=443 --hostlist="%LISTS%list-google.txt" --ip-id=zero --dpi-desync=fake,multidisorder --dpi-desync-fake-tls=^! --new --filter-udp=443 --dpi-desync=fake`
	if args != expected {
		t.Fatalf("Expected:\n%s\nGot:\n%s", expected, args)
	}
}

func TestFormatStrategyName(t *testing.T) {
	if formatStrategyName("general") != "General" {
		t.Errorf("Expected General")
	}
	if formatStrategyName("general (EXP)") != "General [EXP]" {
		t.Errorf("Expected General [EXP]")
	}
	if formatStrategyName("general (ALT12)") != "General [ALT12]" {
		t.Errorf("Expected General [ALT12]")
	}
}

func TestLiveSync(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "zapretify-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	listsDir := filepath.Join(tmpDir, "lists")
	summary, err := UpdateUpstreamAssets(listsDir, tmpDir)
	if err != nil {
		t.Fatalf("UpdateUpstreamAssets returned error: %v", err)
	}

	t.Logf("Summary Message: %s", summary.Message)
	t.Logf("Updated files count: %d", len(summary.UpdatedFiles))
	t.Logf("Failed files count: %d", len(summary.FailedFiles))

	// Verify that host lists were written
	for fn := range UpstreamAssetURLs {
		p := filepath.Join(listsDir, fn)
		fi, err := os.Stat(p)
		if err != nil || fi.Size() == 0 {
			t.Errorf("List file %s was not downloaded or is empty", fn)
		}
	}

	// Verify that strategies.json was written
	stratPath := filepath.Join(tmpDir, "strategies.json")
	fi, err := os.Stat(stratPath)
	if err != nil || fi.Size() == 0 {
		t.Errorf("strategies.json was not created or is empty")
	}
}
