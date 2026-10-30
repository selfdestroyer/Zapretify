package zapret

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"zapretify/core/config"
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

	filePath := filepath.Join(config.AppDir(), "strategies.json")

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

// Find returns the strategy with exactly this ID. Unknown IDs are NOT silently mapped to another strategy.
func (r *StrategyRegistry) Find(id string) (*Strategy, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.Strategies {
		if s.ID == id {
			cpy := s
			return &cpy, true
		}
	}
	return nil, false
}

// Default returns the first (default) strategy of the registry.
func (r *StrategyRegistry) Default() (*Strategy, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.Strategies) == 0 {
		return nil, false
	}
	cpy := r.Strategies[0]
	return &cpy, true
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

	if gameTCPRange == "" || config.ValidatePortRange(gameTCPRange) != nil {
		gameTCPRange = "1024-65535"
	}
	if gameUDPRange == "" || config.ValidatePortRange(gameUDPRange) != nil {
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

	raw := unescapeCaret(rawArgs)
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

// unescapeCaret drops cmd.exe escape carets the way the Flowseal .bat files rely on:
// outside double quotes "^x" becomes "x" (e.g. --dpi-desync-fake-tls=^! -> !), inside quotes "^" is literal.
// winws is started without cmd.exe, so nobody else would remove them.
func unescapeCaret(s string) string {
	if !strings.Contains(s, "^") {
		return s
	}
	var b strings.Builder
	inQuotes := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inQuotes = !inQuotes
		case c == '^' && !inQuotes:
			i++
			if i < len(s) {
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
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
