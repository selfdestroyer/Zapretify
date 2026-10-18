//go:build !windows

package zapret

func GetServiceStatus() (string, string, error) {
	return "NOT_SUPPORTED", "", nil
}

func InstallService(strategyID string, binDir string, listsDir string, gameFilterMode string, gameTCPRange string, gameUDPRange string) error {
	return nil
}

func RemoveService(binDir string) error {
	return nil
}

func StartService(binDir string) error {
	return nil
}

func StopService(binDir string) error {
	return nil
}
