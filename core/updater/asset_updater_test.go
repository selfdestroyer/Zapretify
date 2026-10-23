package updater

import (
	"net/http"
	"net/http/httptest"
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
	if testing.Short() {
		t.Skip("network test skipped in -short mode")
	}
	if os.Getenv("ZAPRETIFY_LIVE_TESTS") == "" {
		t.Skip("set ZAPRETIFY_LIVE_TESTS=1 to run the live upstream sync test")
	}
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

func TestFetchLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte("example.com\n"))
		case "/empty":
			_, _ = w.Write([]byte("  \n"))
		case "/big":
			_, _ = w.Write(make([]byte, 2048))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	if _, err := fetchLimited(srv.Client(), srv.URL+"/ok", 1024); err != nil {
		t.Fatalf("ok: %v", err)
	}
	for _, p := range []string{"/empty", "/big", "/missing"} {
		if _, err := fetchLimited(srv.Client(), srv.URL+p, 1024); err == nil {
			t.Fatalf("%s must be rejected", p)
		}
	}
}

func TestValidRawArgs(t *testing.T) {
	if !validRawArgs(`--wf-tcp=80,443 --hostlist="%LISTS%x.txt"`) {
		t.Fatal("normal args rejected")
	}
	if validRawArgs("--wf-tcp=80\n--evil") {
		t.Fatal("newline must be rejected")
	}
}

func TestDescribeListChange(t *testing.T) {
	oldData := []byte("# comment\na.com\nb.com\n")
	newData := []byte("a.com\nc.com\nd.com\n")
	got := describeListChange("list-general.txt", true, oldData, newData)
	want := "Обновлён основной доменный лист (list-general.txt): +2 / −1 записи"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := describeListChange("list-google.txt", false, nil, newData); got != "Загружен доменный лист Google (list-google.txt)" {
		t.Fatalf("new file: %q", got)
	}
}

func TestCurrentListContentIPSetBackup(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "ipset-all.txt"), []byte("203.0.113.113/32\n"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "ipset-all.txt.backup"), []byte("1.2.3.0/24\n"), 0644)
	data, ok := currentListContent(dir, "ipset-all.txt")
	if !ok || string(data) != "1.2.3.0/24\n" {
		t.Fatalf("expected backup content, got %q (ok=%v)", data, ok)
	}
	if _, ok := currentListContent(dir, "list-general.txt"); ok {
		t.Fatal("missing file must report !ok")
	}
}
