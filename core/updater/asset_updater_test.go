package updater

import (
	"encoding/json"
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

func TestCurrentListContent(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "ipset-all.txt.backup"), []byte("1.2.3.0/24\n"), 0644)
	data, ok := currentListContent(dir, "ipset-all.txt.backup")
	if !ok || string(data) != "1.2.3.0/24\n" {
		t.Fatalf("got %q (ok=%v)", data, ok)
	}
	if _, ok := currentListContent(dir, "list-general.txt"); ok {
		t.Fatal("missing file must report !ok")
	}
}

func TestIPSetSourceIsRealList(t *testing.T) {
	if _, ok := UpstreamAssetURLs["ipset-all.txt"]; ok {
		t.Fatal("ipset-all.txt upstream is only the placeholder and must not be synced")
	}
	if _, ok := UpstreamAssetURLs["ipset-all.txt.backup"]; !ok {
		t.Fatal("the real IPSet must be synced into ipset-all.txt.backup")
	}
}

func TestGitBlobSHA(t *testing.T) {
	// Well-known git ids of the empty blob and of "hello\n".
	if got := gitBlobSHA(nil); got != "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391" {
		t.Fatalf("empty blob: %s", got)
	}
	if got := gitBlobSHA([]byte("hello\n")); got != "ce013625030ba8dba906f756967f9e9ca394464a" {
		t.Fatalf("hello blob: %s", got)
	}
}

func TestReferencedBinsAndNames(t *testing.T) {
	args := `--dpi-desync-fake-tls="%BIN%stun.bin" --dpi-desync-fake-quic="%BIN%quic_initial_www_google_com.bin" --hostlist="%LISTS%list-google.txt"`
	got := referencedBins(args)
	if len(got) != 2 || got[0] != "stun.bin" || got[1] != "quic_initial_www_google_com.bin" {
		t.Fatalf("referencedBins: %v", got)
	}
	for _, bad := range []string{"winws.exe", "WinDivert.dll", "../evil.bin", `a\b.bin`, "x.bin.exe"} {
		if validBinName(bad) {
			t.Fatalf("%q must be rejected", bad)
		}
	}
	if !validBinName("tls_clienthello_www_sferum_ru.bin") {
		t.Fatal("normal fake name rejected")
	}
}

func TestSyncFakeBins(t *testing.T) {
	newFake := []byte("new fake payload")
	changed := []byte("changed payload")
	tampered := []byte("tampered")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/bin":
			_ = json.NewEncoder(w).Encode([]upstreamBin{
				{Name: "new.bin", SHA: gitBlobSHA(newFake), Size: int64(len(newFake))},
				{Name: "same.bin", SHA: gitBlobSHA([]byte("same")), Size: 4},
				{Name: "changed.bin", SHA: gitBlobSHA(changed), Size: int64(len(changed))},
				{Name: "bad.bin", SHA: gitBlobSHA([]byte("expected")), Size: 8},
				{Name: "winws.exe", SHA: "x", Size: 10},
			})
		case "/raw/bin/new.bin":
			_, _ = w.Write(newFake)
		case "/raw/bin/changed.bin":
			_, _ = w.Write(changed)
		case "/raw/bin/bad.bin":
			_, _ = w.Write(tampered)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	oldRaw, oldAPI := flowsealRawBase, flowsealAPIBase
	flowsealRawBase, flowsealAPIBase = srv.URL+"/raw/", srv.URL+"/api/"
	defer func() { flowsealRawBase, flowsealAPIBase = oldRaw, oldAPI }()

	binDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(binDir, "same.bin"), []byte("same"), 0644)
	_ = os.WriteFile(filepath.Join(binDir, "changed.bin"), []byte("old"), 0644)

	summary := &AssetUpdateSummary{}
	added, updated := syncFakeBins(srv.Client(), binDir, nil, summary)
	if len(added) != 1 || added[0] != "new.bin" {
		t.Fatalf("added: %v", added)
	}
	if len(updated) != 1 || updated[0] != "changed.bin" {
		t.Fatalf("updated: %v", updated)
	}
	if len(summary.FailedFiles) != 1 || summary.FailedFiles[0] != "bin/bad.bin" {
		t.Fatalf("failed: %v", summary.FailedFiles)
	}
	if _, err := os.Stat(filepath.Join(binDir, "bad.bin")); err == nil {
		t.Fatal("a download with a wrong checksum must not be written")
	}
	if _, err := os.Stat(filepath.Join(binDir, "winws.exe")); err == nil {
		t.Fatal("executables must never be synced")
	}
}
