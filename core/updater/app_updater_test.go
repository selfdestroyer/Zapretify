package updater

import "testing"

func TestPickAsset(t *testing.T) {
	assets := []ReleaseAsset{
		{Name: "SHA256SUMS.txt"},
		{Name: "Zapretify-macOS-v1.5.0.zip"},
		{Name: "Zapretify-macOS-v1.5.0.dmg"},
		{Name: "Zapretify-Setup-v1.5.0.exe"},
		{Name: "Zapretify-Portable-v1.5.0.zip"},
	}
	if a := pickAsset(assets, "darwin"); a == nil || a.Name != "Zapretify-macOS-v1.5.0.dmg" {
		t.Fatalf("darwin: %+v", a)
	}
	if a := pickAsset(assets, "windows"); a == nil || a.Name != "Zapretify-Setup-v1.5.0.exe" {
		t.Fatalf("windows: %+v", a)
	}
	// A Windows-only release (1.4.x) has nothing for macOS: the UI opens the release page.
	if a := pickAsset(assets[3:], "darwin"); a != nil {
		t.Fatalf("darwin without dmg: %+v", a)
	}
}
