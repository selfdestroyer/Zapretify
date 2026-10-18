//go:build windows

package main

import (
	"strings"
	"testing"
)

func TestCleanupScriptNeverRemovesWholeFolderRecursively(t *testing.T) {
	script := buildCleanupScript(1234, `C:\Users\me\Downloads`, "Zapretify.exe")

	for _, line := range strings.Split(script, "\r\n") {
		if strings.HasPrefix(line, "rd /s") && !strings.Contains(line, `\bin"`) && !strings.Contains(line, `\lists"`) && !strings.Contains(line, `\assets"`) {
			t.Fatalf("recursive removal of an unexpected path: %s", line)
		}
	}
	if !strings.Contains(script, "rd \"C:\\Users\\me\\Downloads\" >nul") {
		t.Fatalf("application folder must be removed with a non-recursive rd")
	}
	if strings.Contains(script, "/s /q \"C:\\Users\\me\\Downloads\"") {
		t.Fatalf("whole application folder must never be removed recursively")
	}
}
