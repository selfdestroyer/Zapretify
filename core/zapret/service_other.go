//go:build !windows

package zapret

func GetServiceStatus() (string, string, error) {
	return "NOT_SUPPORTED", "", nil
}

func InstallService(strategyID string, binDir string, listsDir string, gameFilterMode string, gameTCPRange string, gameUDPRange string) error {
	return nil
}

func RemoveService() error {
	return nil
}

func StartService() error {
	return nil
}

func StopService() error {
	return nil
}
