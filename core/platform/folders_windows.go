//go:build windows

package platform

import "golang.org/x/sys/windows"

func knownFolder(id *windows.KNOWNFOLDERID) string {
	p, err := windows.KnownFolderPath(id, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return ""
	}
	return p
}

// DesktopDir returns the real Desktop folder of the current user (follows OneDrive redirection and
// does not depend on %USERPROFILE%). Empty string if it cannot be determined.
func DesktopDir() string { return knownFolder(windows.FOLDERID_Desktop) }

// StartMenuProgramsDir returns the current user's Start Menu\Programs folder.
func StartMenuProgramsDir() string { return knownFolder(windows.FOLDERID_Programs) }

// PublicDesktopDir returns the shared Desktop folder.
func PublicDesktopDir() string { return knownFolder(windows.FOLDERID_PublicDesktop) }

// CommonStartMenuProgramsDir returns the all-users Start Menu\Programs folder.
func CommonStartMenuProgramsDir() string { return knownFolder(windows.FOLDERID_CommonPrograms) }
