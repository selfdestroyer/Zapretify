//go:build !windows

package platform

const (
	MB_OK           = 0x00000000
	MB_OKCANCEL     = 0x00000001
	MB_YESNO        = 0x00000004
	MB_ICONINFO     = 0x00000040
	MB_ICONWARN     = 0x00000030
	MB_ICONQUESTION = 0x00000020
	MB_ICONERROR    = 0x00000010

	IDOK     = 1
	IDCANCEL = 2
	IDYES    = 6
	IDNO     = 7
)

// ShowMessageBox stub for non-Windows platforms.
func ShowMessageBox(title, text string, style uint32) int {
	return IDYES
}
