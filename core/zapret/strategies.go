package zapret

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Strategy struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	RawArgs     string `json:"raw_args"`
}

type StrategyRegistry struct {
	Version    string     `json:"version"`
	Strategies []Strategy `json:"strategies"`
	mu         sync.RWMutex
}

var (
	registryInstance *StrategyRegistry
	registryOnce     sync.Once
)

func GetRegistry() *StrategyRegistry {
	registryOnce.Do(func() {
		registryInstance = &StrategyRegistry{}
		_ = registryInstance.Load()
	})
	return registryInstance
}

func (r *StrategyRegistry) Load() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	exePath, err := os.Executable()
	if err != nil {
		exePath = "."
	}
	appDir := filepath.Dir(exePath)
	filePath := filepath.Join(appDir, "strategies.json")

	data, err := os.ReadFile(filePath)
	if err != nil {
		// Fallback to local directory
		data, err = os.ReadFile("strategies.json")
		if err != nil {
			return err
		}
	}

	var fileData struct {
		Version    string     `json:"version"`
		Strategies []Strategy `json:"strategies"`
	}
	if err := json.Unmarshal(data, &fileData); err != nil {
		return err
	}
	r.Version = fileData.Version
	r.Strategies = fileData.Strategies
	return nil
}

func (r *StrategyRegistry) List() []Strategy {
	r.mu.RLock()
	defer r.mu.RUnlock()
	res := make([]Strategy, len(r.Strategies))
	copy(res, r.Strategies)
	return res
}

func (r *StrategyRegistry) Find(id string) (*Strategy, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.Strategies {
		if s.ID == id {
			cpy := s
			return &cpy, true
		}
	}
	// Fallback to default "general"
	if len(r.Strategies) > 0 {
		cpy := r.Strategies[0]
		return &cpy, true
	}
	return nil, false
}

// BuildArgs renders the raw arguments into command line tokens for exec.Command
func BuildArgs(
	rawArgs string,
	binDir string,
	listsDir string,
	gameFilterMode string,
	gameTCPRange string,
	gameUDPRange string,
) []string {
	raw := buildProcessedRawArgs(rawArgs, binDir, listsDir, gameFilterMode, gameTCPRange, gameUDPRange)
	return splitArgs(raw)
}

// BuildArgsString formats raw arguments for Windows sc.exe command line
func BuildArgsString(
	rawArgs string,
	binDir string,
	listsDir string,
	gameFilterMode string,
	gameTCPRange string,
	gameUDPRange string,
) string {
	raw := buildProcessedRawArgs(rawArgs, binDir, listsDir, gameFilterMode, gameTCPRange, gameUDPRange)
	return strings.ReplaceAll(raw, `"`, `\"`)
}

func buildProcessedRawArgs(
	rawArgs string,
	binDir string,
	listsDir string,
	gameFilterMode string,
	gameTCPRange string,
	gameUDPRange string,
) string {
	bPath := binDir
	if bPath != "" && !strings.HasSuffix(bPath, "\\") && !strings.HasSuffix(bPath, "/") {
		bPath += string(filepath.Separator)
	}
	lPath := listsDir
	if lPath != "" && !strings.HasSuffix(lPath, "\\") && !strings.HasSuffix(lPath, "/") {
		lPath += string(filepath.Separator)
	}

	if gameTCPRange == "" {
		gameTCPRange = "1024-65535"
	}
	if gameUDPRange == "" {
		gameUDPRange = "1024-65535"
	}

	gameTCP := "12"
	gameUDP := "12"

	switch strings.ToLower(gameFilterMode) {
	case "all":
		gameTCP = gameTCPRange
		gameUDP = gameUDPRange
	case "tcp":
		gameTCP = gameTCPRange
		gameUDP = "12"
	case "udp":
		gameTCP = "12"
		gameUDP = gameUDPRange
	default: // "disabled"
		gameTCP = "12"
		gameUDP = "12"
	}

	raw := rawArgs
	raw = strings.ReplaceAll(raw, "%BIN%", bPath)
	raw = strings.ReplaceAll(raw, "%LISTS%", lPath)
	raw = strings.ReplaceAll(raw, "%GameFilterTCP%", gameTCP)
	raw = strings.ReplaceAll(raw, "%GameFilterUDP%", gameUDP)

	// Clean up empty filter commas
	raw = strings.ReplaceAll(raw, ",,", ",")
	raw = strings.ReplaceAll(raw, ", ", " ")
	raw = strings.ReplaceAll(raw, "=,", "=")

	return raw
}

func splitArgs(cmd string) []string {
	var args []string
	var current strings.Builder
	inQuotes := false

	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch c {
		case '"':
			inQuotes = !inQuotes
		case ' ':
			if inQuotes {
				current.WriteByte(c)
			} else if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(c)
		}
	}

	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}
