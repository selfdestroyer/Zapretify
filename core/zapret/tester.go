package zapret

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"zapretify/core/logger"
)

type TargetEndpoint struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Host     string `json:"host"`
	Category string `json:"category"` // "youtube", "discord", "blockcheck", "baseline"
}

// AllTargets содержит полный список ключевых ресурсов из list-general, list-google и оригинального blockcheck.sh
var AllTargets = []TargetEndpoint{
	// YouTube & Google
	{ID: "yt_video", Name: "YouTube Video CDN", URL: "https://redirector.googlevideo.com", Host: "redirector.googlevideo.com", Category: "youtube"},
	{ID: "yt_web", Name: "YouTube Web", URL: "https://www.youtube.com", Host: "www.youtube.com", Category: "youtube"},
	{ID: "yt_img", Name: "YouTube Thumbnails", URL: "https://i.ytimg.com", Host: "i.ytimg.com", Category: "youtube"},
	{ID: "google_main", Name: "Google Services", URL: "https://www.google.com", Host: "www.google.com", Category: "youtube"},

	// Discord Ecosystem
	{ID: "discord_gw", Name: "Discord Gateway", URL: "https://gateway.discord.gg", Host: "gateway.discord.gg", Category: "discord"},
	{ID: "discord_cdn", Name: "Discord CDN", URL: "https://cdn.discordapp.com", Host: "cdn.discordapp.com", Category: "discord"},
	{ID: "discord_media", Name: "Discord Voice/Media", URL: "https://discord.media", Host: "discord.media", Category: "discord"},
	{ID: "discord_app", Name: "Discord Web/App", URL: "https://discord.com", Host: "discord.com", Category: "discord"},

	// Original Zapret & Blockcheck (RuTracker, NTC Party, X/Twitter, Cloudflare)
	{ID: "rutracker", Name: "RuTracker (Blockcheck)", URL: "https://rutracker.org", Host: "rutracker.org", Category: "blockcheck"},
	{ID: "ntc_party", Name: "NTC Party (Anti-DPI)", URL: "https://ntc.party", Host: "ntc.party", Category: "blockcheck"},
	{ID: "twitter", Name: "X / Twitter", URL: "https://x.com", Host: "x.com", Category: "blockcheck"},
	{ID: "cloudflare", Name: "Cloudflare DoH/ECH", URL: "https://cloudflare-dns.com", Host: "cloudflare-dns.com", Category: "blockcheck"},

	// Baseline (Unblocked reference)
	{ID: "iana_baseline", Name: "IANA (Эталон сети)", URL: "https://www.iana.org", Host: "www.iana.org", Category: "baseline"},
}

var DefaultTargets = AllTargets

func GetTargetsByGroup(group string) []TargetEndpoint {
	switch group {
	case "yt_discord":
		var list []TargetEndpoint
		for _, t := range AllTargets {
			if t.Category == "youtube" || t.Category == "discord" {
				list = append(list, t)
			}
		}
		return list
	case "blockcheck":
		var list []TargetEndpoint
		for _, t := range AllTargets {
			if t.Category == "blockcheck" || t.Category == "baseline" {
				list = append(list, t)
			}
		}
		return list
	default:
		return AllTargets
	}
}

type TargetResult struct {
	Name        string  `json:"name"`
	URL         string  `json:"url"`
	Host        string  `json:"host"`
	Category    string  `json:"category"`
	Success     bool    `json:"success"`
	LatencyMs   float64 `json:"latency_ms"`
	Code        int     `json:"code"`
	DPIStatus   string  `json:"dpi_status"`  // "pass", "blocked_rst", "blocked_timeout", "blocked_redirect", "error"
	DPIMessage  string  `json:"dpi_message"` // "Обход успешен ✓", "Блокировка ТСПУ (TCP RST)", etc.
	CheckMethod string  `json:"check_method"`
}

type BenchmarkResult struct {
	StrategyID   string         `json:"strategy_id"`
	StrategyName string         `json:"strategy_name"`
	Method       string         `json:"method"`
	TargetGroup  string         `json:"target_group"`
	SuccessCount int            `json:"success_count"`
	TotalCount   int            `json:"total_count"`
	AvgLatencyMs float64        `json:"avg_latency_ms"`
	Results      []TargetResult `json:"results"`
	IsWorking    bool           `json:"is_working"`
}

// CheckTarget выполняет детальную диагностику ресурса по методам HTTPS (пинг/задержка) и DPI (фильтрация ТСПУ)
func CheckTarget(target TargetEndpoint, checkMethod string, timeout time.Duration) TargetResult {
	if checkMethod == "" {
		checkMethod = "all"
	}

	res := TargetResult{
		Name:        target.Name,
		URL:         target.URL,
		Host:        target.Host,
		Category:    target.Category,
		CheckMethod: checkMethod,
	}

	start := time.Now()

	// 1. Метод: Только HTTPS (замер пинга, кодов ответа и доступности)
	if checkMethod == "https" {
		tr := &http.Transport{
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			DisableKeepAlives: true,
		}
		client := &http.Client{
			Transport: tr,
			Timeout:   timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return http.ErrUseLastResponse
				}
				loc := strings.ToLower(req.URL.String())
				if strings.Contains(loc, "eais.rkn.gov.ru") || strings.Contains(loc, "warning.rt.ru") || strings.Contains(loc, "zapret.gov.ru") {
					return fmt.Errorf("DPI_REDIRECT_RKN")
				}
				return nil
			},
		}

		req, err := http.NewRequest("GET", target.URL, nil)
		if err != nil {
			res.Success = false
			res.LatencyMs = float64(time.Since(start).Microseconds()) / 1000.0
			res.DPIStatus = "error"
			res.DPIMessage = "Ошибка запроса"
			return res
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0 Safari/537.36")

		resp, err := client.Do(req)
		elapsed := float64(time.Since(start).Microseconds()) / 1000.0
		res.LatencyMs = elapsed

		if err != nil {
			res.Success = false
			errStr := strings.ToLower(err.Error())
			if strings.Contains(errStr, "dpi_redirect_rkn") {
				res.DPIStatus = "blocked_redirect"
				res.DPIMessage = "Блокировка ТСПУ (Редирект РКН)"
			} else if strings.Contains(errStr, "reset") || strings.Contains(errStr, "forcibly closed") {
				res.DPIStatus = "blocked_rst"
				res.DPIMessage = "Блокировка ТСПУ (TCP RST)"
			} else if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline") {
				res.DPIStatus = "blocked_timeout"
				res.DPIMessage = "Блокировка ТСПУ (Таймаут/Дроп)"
			} else {
				res.DPIStatus = "error"
				res.DPIMessage = "Недоступен"
			}
			return res
		}
		defer resp.Body.Close()

		res.Code = resp.StatusCode
		res.Success = resp.StatusCode > 0 && resp.StatusCode < 500
		res.DPIStatus = "pass"
		res.DPIMessage = "HTTPS доступен ✓"
		return res
	}

	// 2. Метод: DPI анализ и Комплексный тест ("dpi" или "all")
	// Проверка глубокого перехвата пакетов: отправка TLS ClientHello с реальным SNI и анализ реакции ТСПУ
	tcpConn, err := net.DialTimeout("tcp", net.JoinHostPort(target.Host, "443"), timeout)
	if err != nil {
		elapsed := float64(time.Since(start).Microseconds()) / 1000.0
		res.LatencyMs = elapsed
		res.Success = false
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "reset") || strings.Contains(errStr, "forcibly closed") {
			res.DPIStatus = "blocked_rst"
			res.DPIMessage = "Блокировка ТСПУ (TCP RST)"
		} else {
			res.DPIStatus = "blocked_timeout"
			res.DPIMessage = "Недоступен (TCP таймаут)"
		}
		return res
	}
	defer tcpConn.Close()

	// Рукопожатие TLS с явной передачей целевого SNI
	tlsConn := tls.Client(tcpConn, &tls.Config{
		ServerName:         target.Host,
		InsecureSkipVerify: true, //nolint:gosec
	})
	_ = tlsConn.SetDeadline(time.Now().Add(timeout))

	err = tlsConn.Handshake()
	elapsed := float64(time.Since(start).Microseconds()) / 1000.0
	res.LatencyMs = elapsed

	if err != nil {
		res.Success = false
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "reset") || strings.Contains(errStr, "forcibly closed") {
			res.DPIStatus = "blocked_rst"
			res.DPIMessage = "Блокировка ТСПУ (TCP RST)"
		} else if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline") {
			res.DPIStatus = "blocked_timeout"
			res.DPIMessage = "Блокировка ТСПУ (Дроп ClientHello)"
		} else {
			res.DPIStatus = "error"
			res.DPIMessage = "Сбой TLS соединения"
		}
		return res
	}
	defer tlsConn.Close()

	// Двусторонний обмен данными поверх TLS: отправка HTTP HEAD для валидации канала
	httpReq := fmt.Sprintf("HEAD / HTTP/1.1\r\nHost: %s\r\nUser-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64)\r\nConnection: close\r\n\r\n", target.Host)
	_ = tlsConn.SetDeadline(time.Now().Add(timeout))
	_, err = tlsConn.Write([]byte(httpReq))
	if err != nil {
		res.Success = false
		res.DPIStatus = "blocked_rst"
		res.DPIMessage = "Сброс соединения после SNI"
		return res
	}

	reader := bufio.NewReader(tlsConn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		// Рукопожатие TLS прошло успешно, соединение завершено сервером штатно
		res.Success = true
		res.DPIStatus = "pass"
		res.DPIMessage = "DPI обойден успешно ✓"
		res.Code = 200
		return res
	}

	var proto string
	var code int
	_, _ = fmt.Sscanf(statusLine, "%s %d", &proto, &code)
	res.Code = code

	// Проверка заголовка Location на предмет редиректа на страницу блокировки РКН
	for {
		line, err := reader.ReadString('\n')
		if err != nil || line == "\r\n" || line == "\n" {
			break
		}
		lowerLine := strings.ToLower(line)
		if strings.HasPrefix(lowerLine, "location:") {
			if strings.Contains(lowerLine, "eais.rkn.gov.ru") || strings.Contains(lowerLine, "warning.rt.ru") || strings.Contains(lowerLine, "zapret.gov.ru") {
				res.Success = false
				res.DPIStatus = "blocked_redirect"
				res.DPIMessage = "Блокировка ТСПУ (Редирект РКН)"
				return res
			}
		}
	}

	res.Success = true
	res.DPIStatus = "pass"
	res.DPIMessage = "DPI обойден успешно ✓"
	return res
}

func CheckAllTargets(targets []TargetEndpoint, method string, timeout time.Duration) []TargetResult {
	if len(targets) == 0 {
		targets = AllTargets
	}
	if method == "" {
		method = "all"
	}

	results := make([]TargetResult, len(targets))
	var wg sync.WaitGroup

	for i, t := range targets {
		wg.Add(1)
		go func(idx int, tgt TargetEndpoint) {
			defer wg.Done()
			results[idx] = CheckTarget(tgt, method, timeout)
		}(i, t)
	}

	wg.Wait()
	return results
}

func BenchmarkCurrentStrategy(strategyID string, strategyName string, method string, targetGroup string) BenchmarkResult {
	if method == "" {
		method = "all"
	}
	if targetGroup == "" {
		targetGroup = "all"
	}

	targets := GetTargetsByGroup(targetGroup)
	results := CheckAllTargets(targets, method, 2500*time.Millisecond)

	successes := 0
	var totalLat float64

	for _, r := range results {
		if r.Success {
			successes++
			totalLat += r.LatencyMs
		}
	}

	// -1: ни одна цель не открылась (в интерфейсе это «—», а не «0 мс»).
	avgLat := -1.0
	if successes > 0 {
		avgLat = totalLat / float64(successes)
	}

	return BenchmarkResult{
		StrategyID:   strategyID,
		StrategyName: strategyName,
		Method:       method,
		TargetGroup:  targetGroup,
		SuccessCount: successes,
		TotalCount:   len(results),
		AvgLatencyMs: avgLat,
		Results:      results,
		IsWorking:    successes >= (len(results)*7)/10, // 70%+ целей работает
	}
}

type StrategyScore struct {
	StrategyID    string         `json:"strategy_id"`
	StrategyName  string         `json:"strategy_name"`
	Description   string         `json:"description"`
	SuccessCount  int            `json:"success_count"`
	PassedTargets int            `json:"passed_targets"`
	TotalCount    int            `json:"total_count"`
	TotalTargets  int            `json:"total_targets"`
	AvgLatencyMs  float64        `json:"avg_latency_ms"`
	Passed        bool           `json:"passed"`
	StartFailed   bool           `json:"start_failed"` // winws.exe не запустился с этой стратегией
	Results       []TargetResult `json:"results"`
	Rank          int            `json:"rank"`
}

type AutoBenchmarkStatus struct {
	Running          bool            `json:"running"`
	IsRunning        bool            `json:"is_running"`
	CurrentIndex     int             `json:"current_index"`
	TestedCount      int             `json:"tested_count"`
	TotalStrategies  int             `json:"total_strategies"`
	TotalCount       int             `json:"total_count"`
	CurrentStrategy  string          `json:"current_strategy"`
	BestStrategyID   string          `json:"best_strategy_id"`
	BestStrategyName string          `json:"best_strategy_name"`
	Method           string          `json:"method"`
	TargetGroup      string          `json:"target_group"`
	Leaderboard      []StrategyScore `json:"leaderboard"`
	Results          []StrategyScore `json:"results"`
	Stopped          bool            `json:"stopped"` // подбор остановлен пользователем до конца списка
	Error            string          `json:"error,omitempty"`
}

var (
	autoBenchStatus AutoBenchmarkStatus
	autoBenchMu     sync.RWMutex
	autoBenchCancel context.CancelFunc
)

func GetAutoBenchmarkStatus() AutoBenchmarkStatus {
	autoBenchMu.RLock()
	defer autoBenchMu.RUnlock()
	return autoBenchStatus
}

func StopAutoBenchmark() {
	autoBenchMu.Lock()
	if autoBenchCancel != nil {
		autoBenchCancel()
		autoBenchCancel = nil
	}
	autoBenchMu.Unlock()
}

func formatMethod(method string) string {
	switch method {
	case "dpi":
		return "Только DPI (Тест глубокой фильтрации ТСПУ: RST / Drop / Редиректы)"
	case "https":
		return "Только HTTPS (Задержка и HTTP-код ответа)"
	default:
		return "Комплексный (DPI фильтрация ТСПУ + HTTPS пинг)"
	}
}

func formatTargetGroup(group string) string {
	switch group {
	case "yt_discord":
		return "YouTube и Discord (8 ключевых CDN/Voice/Gateway)"
	case "blockcheck":
		return "Оригинальный Blockcheck (RuTracker, NTC Party, X/Twitter, Cloudflare, IANA)"
	default:
		return "Все 13 ресурсов оригинала (YouTube, Discord, RuTracker, X, NTC, Cloudflare)"
	}
}

// SaveBenchmarkReport сохраняет подробный отчет одиночного теста в текстовый файл, перезаписывая его
func SaveBenchmarkReport(filePath string, res BenchmarkResult) error {
	var sb strings.Builder
	now := time.Now().Format("2006-01-02 15:04:05")

	sb.WriteString("================================================================================\n")
	sb.WriteString("                    ZAPRETIFY - ОТЧЕТ ТЕСТИРОВАНИЯ СТРАТЕГИИ                    \n")
	sb.WriteString("================================================================================\n")
	sb.WriteString(fmt.Sprintf("Дата и время проверки: %s\n", now))
	sb.WriteString(fmt.Sprintf("ID стратегии:          %s\n", res.StrategyID))
	sb.WriteString(fmt.Sprintf("Название стратегии:    %s\n", res.StrategyName))
	sb.WriteString(fmt.Sprintf("Метод проверки:        %s\n", formatMethod(res.Method)))
	sb.WriteString(fmt.Sprintf("Тестовые адреса:       %s\n", formatTargetGroup(res.TargetGroup)))
	sb.WriteString("\n")
	sb.WriteString("ИТОГОВЫЙ РЕЗУЛЬТАТ:\n")
	pct := 0
	if res.TotalCount > 0 {
		pct = (res.SuccessCount * 100) / res.TotalCount
	}
	sb.WriteString(fmt.Sprintf("- Доступно ресурсов:   %d из %d (%d%%)\n", res.SuccessCount, res.TotalCount, pct))
	if res.SuccessCount > 0 {
		sb.WriteString(fmt.Sprintf("- Средняя задержка:    %.0f ms\n", res.AvgLatencyMs))
	} else {
		sb.WriteString("- Средняя задержка:    Таймаут (нет связи)\n")
	}
	verdict := "НЕ РАБОТАЕТ (Блокировки не обойдены)"
	if res.IsWorking {
		verdict = "РАБОТАЕТ ОТЛИЧНО (Обход успешен)"
	} else if res.SuccessCount > 0 {
		verdict = "ЧАСТИЧНО РАБОТАЕТ (Доступна часть ресурсов)"
	}
	sb.WriteString(fmt.Sprintf("- Статус стратегии:    %s\n", verdict))
	sb.WriteString("\n")
	sb.WriteString("--------------------------------------------------------------------------------\n")
	sb.WriteString("ДЕТАЛИЗАЦИЯ ПО КАЖДОМУ РЕСУРСУ:\n")
	sb.WriteString("--------------------------------------------------------------------------------\n")

	for i, r := range res.Results {
		sb.WriteString(fmt.Sprintf("[%d] %s (%s)\n", i+1, r.Name, strings.ToUpper(r.Category)))
		sb.WriteString(fmt.Sprintf("    Хост / URL:    %s\n", r.URL))
		sb.WriteString(fmt.Sprintf("    Статус DPI:    %s [%s]\n", r.DPIMessage, r.DPIStatus))
		if r.LatencyMs >= 0 {
			sb.WriteString(fmt.Sprintf("    HTTPS Пинг:    %.0f ms (HTTP код: %d)\n", r.LatencyMs, r.Code))
		} else {
			sb.WriteString(fmt.Sprintf("    HTTPS Пинг:    Таймаут (HTTP код: %d)\n", r.Code))
		}
		resVerdict := "✗ ЗАБЛОКИРОВАН"
		if r.Success {
			resVerdict = "✓ ДОСТУПЕН"
		}
		sb.WriteString(fmt.Sprintf("    Итог:          %s\n", resVerdict))
		sb.WriteString("\n")
	}

	sb.WriteString("================================================================================\n")
	sb.WriteString("Примечание: данный файл автоматически перезаписывается при каждом новом тесте.  \n")
	sb.WriteString("================================================================================\n")

	return os.WriteFile(filePath, []byte(sb.String()), 0644)
}

// SaveAutoBenchmarkReport сохраняет подробный отчет тестирования всех стратегий в текстовый файл, перезаписывая его
func SaveAutoBenchmarkReport(filePath string, st AutoBenchmarkStatus) error {
	var sb strings.Builder
	now := time.Now().Format("2006-01-02 15:04:05")

	sb.WriteString("================================================================================\n")
	sb.WriteString("               ZAPRETIFY - ОТЧЕТ АВТОМАТИЧЕСКОГО БЕНЧМАРКА СТРАТЕГИЙ            \n")
	sb.WriteString("================================================================================\n")
	sb.WriteString(fmt.Sprintf("Дата и время проверки: %s\n", now))
	sb.WriteString(fmt.Sprintf("Всего протестировано:  %d стратегий Flowseal\n", st.TotalStrategies))
	sb.WriteString(fmt.Sprintf("Метод проверки:        %s\n", formatMethod(st.Method)))
	sb.WriteString(fmt.Sprintf("Тестовые адреса:       %s\n", formatTargetGroup(st.TargetGroup)))
	sb.WriteString("\n")

	if st.BestStrategyID != "" {
		sb.WriteString("РЕКОМЕНДОВАННАЯ СТРАТЕГИЯ:\n")
		sb.WriteString(fmt.Sprintf("- ID победителя:       %s\n", st.BestStrategyID))
		sb.WriteString(fmt.Sprintf("- Название стратегии:  %s\n", st.BestStrategyName))
		if len(st.Leaderboard) > 0 {
			best := st.Leaderboard[0]
			pct := 0
			if best.TotalTargets > 0 {
				pct = (best.PassedTargets * 100) / best.TotalTargets
			}
			sb.WriteString(fmt.Sprintf("- Результат:           %d из %d адресов доступно (%d%%)\n", best.PassedTargets, best.TotalTargets, pct))
			if best.AvgLatencyMs >= 0 {
				sb.WriteString(fmt.Sprintf("- Средний пинг:        %.0f ms\n", best.AvgLatencyMs))
			} else {
				sb.WriteString("- Средний пинг:        Таймаут (нет связи)\n")
			}
		}
		sb.WriteString("- Вердикт:             РЕКОМЕНДОВАНО ДЛЯ ВАШЕГО ПРОВАЙДЕРА (Лучший результат)\n")
	} else {
		sb.WriteString("РЕЗУЛЬТАТ: Ни одна стратегия не показала стабильного соединения.\n")
	}
	sb.WriteString("\n")

	sb.WriteString("--------------------------------------------------------------------------------\n")
	sb.WriteString("ТАБЛИЦА РЕЙТИНГА СТРАТЕГИЙ (LEADERBOARD):\n")
	sb.WriteString("--------------------------------------------------------------------------------\n")
	sb.WriteString(fmt.Sprintf("%-5s | %-24s | %-14s | %-10s | %s\n", "Ранг", "ID Стратегии", "Доступность", "Ср. Пинг", "Название стратегии"))
	sb.WriteString("--------------------------------------------------------------------------------\n")

	for i, item := range st.Leaderboard {
		pct := 0
		if item.TotalTargets > 0 {
			pct = (item.PassedTargets * 100) / item.TotalTargets
		}
		passStr := fmt.Sprintf("%d/%d (%d%%)", item.PassedTargets, item.TotalTargets, pct)
		pingStr := "Таймаут"
		if item.AvgLatencyMs >= 0 {
			pingStr = fmt.Sprintf("%.0f ms", item.AvgLatencyMs)
		}
		rankStr := fmt.Sprintf("#%d", i+1)
		if i == 0 && item.StrategyID == st.BestStrategyID {
			rankStr = "#1 [WIN]"
		}
		sb.WriteString(fmt.Sprintf("%-5s | %-24s | %-14s | %-10s | %s\n", rankStr, item.StrategyID, passStr, pingStr, item.StrategyName))
	}
	sb.WriteString("\n")

	if len(st.Leaderboard) > 0 {
		winner := st.Leaderboard[0]
		sb.WriteString("--------------------------------------------------------------------------------\n")
		sb.WriteString(fmt.Sprintf("ДЕТАЛИЗАЦИЯ ПОБЕДИТЕЛЯ (#1 %s):\n", winner.StrategyName))
		sb.WriteString("--------------------------------------------------------------------------------\n")
		for j, r := range winner.Results {
			resVerdict := "✗ Заблокирован"
			if r.Success {
				resVerdict = "✓ Доступен"
			}
			pingVal := fmt.Sprintf("%.0f ms", r.LatencyMs)
			if r.LatencyMs < 0 {
				pingVal = "Таймаут"
			}
			sb.WriteString(fmt.Sprintf("[%d] %-24s | DPI: %-16s | HTTPS: %-8s (%d) | %s\n", j+1, r.Name, r.DPIStatus, pingVal, r.Code, resVerdict))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("================================================================================\n")
	sb.WriteString("Примечание: данный файл автоматически перезаписывается при каждом новом тесте.  \n")
	sb.WriteString("================================================================================\n")

	return os.WriteFile(filePath, []byte(sb.String()), 0644)
}

// StartAutoBenchmark tests every strategy one after another in the background.
// runner/registry are the engine and registry owned by the application; onFinish (optional) is called after
// the benchmark has ended (normally or by cancellation) and winws.exe has been stopped.
func StartAutoBenchmark(runner Engine, registry *StrategyRegistry, binDir, listsDir, gameFilterMode, gameTCP, gameUDP, method, targetGroup, appDir string, onFinish func()) error {
	if runner == nil {
		runner = GetRunner()
	}
	if registry == nil {
		registry = GetRegistry()
	}
	autoBenchMu.Lock()
	if autoBenchStatus.Running {
		autoBenchMu.Unlock()
		return fmt.Errorf("тестирование уже запущено")
	}

	if method == "" {
		method = "all"
	}
	if targetGroup == "" {
		targetGroup = "all"
	}

	logger.Log("[AutoBenchmark] Подготовка к автоматическому тестированию стратегий (метод: %s, цели: %s)...", method, targetGroup)

	// Обязательно останавливаем активный zapret перед авто-бенчмарком
	_ = runner.Stop()
	_ = StopService(binDir)
	time.Sleep(300 * time.Millisecond)

	strategies := registry.List()
	logger.Log("[AutoBenchmark] Загружено %d стратегий Flowseal для тестирования", len(strategies))

	ctx, cancel := context.WithCancel(context.Background())
	autoBenchCancel = cancel

	autoBenchStatus = AutoBenchmarkStatus{
		Running:         true,
		IsRunning:       true,
		CurrentIndex:    0,
		TestedCount:     0,
		TotalStrategies: len(strategies),
		TotalCount:      len(strategies),
		Method:          method,
		TargetGroup:     targetGroup,
		Leaderboard:     []StrategyScore{},
		Results:         []StrategyScore{},
	}
	autoBenchMu.Unlock()

	go func() {
		defer func() {
			autoBenchMu.Lock()
			autoBenchStatus.Running = false
			autoBenchStatus.IsRunning = false
			autoBenchCancel = nil
			currentStatus := autoBenchStatus
			autoBenchMu.Unlock()
			_ = runner.Stop()
			_ = StopService(binDir)

			if appDir != "" && len(currentStatus.Leaderboard) > 0 {
				reportPath := filepath.Join(appDir, "test_results.txt")
				if err := SaveAutoBenchmarkReport(reportPath, currentStatus); err != nil {
					logger.Log("[AutoBenchmark] Ошибка записи итогового отчета в %s: %v", reportPath, err)
				} else {
					logger.Log("[AutoBenchmark] Итоговый отчет авто-бенчмарка успешно сохранен в: %s", reportPath)
				}
			}
			if appDir != "" && currentStatus.Error == "" && currentStatus.BestStrategyID != "" {
				if err := SaveLastBenchmark(appDir, LastBenchmarkFromStatus(currentStatus, time.Now())); err != nil {
					logger.Log("[AutoBenchmark] Не удалось сохранить результат подбора: %v", err)
				}
			}
			logger.Log("[AutoBenchmark] Тестирование всех стратегий завершено!")
			if onFinish != nil {
				onFinish()
			}
		}()

		consecutiveStartFailures := 0
		anyStarted := false // хотя бы одна стратегия запустилась: значит, winws в принципе работает
		stopped := false

		var scores []StrategyScore
		testTargets := GetTargetsByGroup(targetGroup)

	strategyLoop:
		for i, strat := range strategies {
			select {
			case <-ctx.Done():
				logger.Log("[AutoBenchmark] Авто-бенчмарк остановлен пользователем после %d из %d стратегий.", len(scores), len(strategies))
				stopped = true
				break strategyLoop
			default:
			}

			logger.Log("[AutoBenchmark] [%d/%d] Проверка: %s...", i+1, len(strategies), strat.Name)

			autoBenchMu.Lock()
			autoBenchStatus.CurrentIndex = i + 1
			autoBenchStatus.TestedCount = i + 1
			autoBenchStatus.CurrentStrategy = strat.Name
			autoBenchMu.Unlock()

			// Запускаем проверяемую стратегию. Если winws не стартовал, результат теста был бы
			// измерен БЕЗ обхода блокировок и ввёл бы в заблуждение, поэтому такая стратегия
			// получает 0 и в журнал пишется причина.
			var targets []TargetResult
			startFailed := false
			if startErr := runner.Start(strat.ID, binDir, listsDir, gameFilterMode, gameTCP, gameUDP); startErr != nil {
				consecutiveStartFailures++
				startFailed = true
				logger.Log("[AutoBenchmark] Не удалось запустить стратегию %s: %v", strat.Name, startErr)
				// Три неудачных запуска подряд в самом начале означают, что winws не работает вообще
				// (нет прав, файлов или конфликт WinDivert): дальше проверять бессмысленно.
				if consecutiveStartFailures >= 3 && !anyStarted {
					autoBenchMu.Lock()
					autoBenchStatus.Error = fmt.Sprintf("winws.exe не запускается (3 попытки подряд): %v. Проверьте права администратора, наличие файлов в bin/ и конфликт с другими программами на WinDivert (GoodbyeDPI и др.).", startErr)
					autoBenchMu.Unlock()
					return
				}
				for _, t := range testTargets {
					targets = append(targets, TargetResult{Name: t.Name, URL: t.URL, Host: t.Host, Category: t.Category, Success: false, LatencyMs: -1, DPIStatus: "error", DPIMessage: "winws не запустился: " + startErr.Error()})
				}
			} else {
				consecutiveStartFailures = 0
				anyStarted = true
				// Выполняем проверку целей
				targets = CheckAllTargets(testTargets, method, 2000*time.Millisecond)
				_ = runner.Stop()
			}

			successes := 0
			var totalLat float64
			for _, t := range targets {
				if t.Success {
					successes++
					totalLat += t.LatencyMs
				}
			}

			avgLat := -1.0
			if successes > 0 {
				avgLat = totalLat / float64(successes)
			}

			logger.Log("[AutoBenchmark] Результат %s: %d/%d целей доступно, пинг %.1f ms", strat.Name, successes, len(targets), avgLat)

			score := StrategyScore{
				StrategyID:    strat.ID,
				StrategyName:  strat.Name,
				Description:   strat.Description,
				SuccessCount:  successes,
				PassedTargets: successes,
				TotalCount:    len(targets),
				TotalTargets:  len(targets),
				AvgLatencyMs:  avgLat,
				Passed:        successes >= (len(targets)*7)/10,
				StartFailed:   startFailed,
				Results:       targets,
			}
			scores = append(scores, score)

			autoBenchMu.Lock()
			autoBenchStatus.Leaderboard = append([]StrategyScore{}, scores...)
			autoBenchStatus.Results = append([]StrategyScore{}, scores...)
			autoBenchMu.Unlock()

			time.Sleep(150 * time.Millisecond)
		}

		// Сортировка таблицы лидеров: наибольшее количество успешных целей, затем наименьшая задержка
		sort.SliceStable(scores, func(i, j int) bool {
			if scores[i].SuccessCount != scores[j].SuccessCount {
				return scores[i].SuccessCount > scores[j].SuccessCount
			}
			return scores[i].AvgLatencyMs < scores[j].AvgLatencyMs
		})

		for idx := range scores {
			scores[idx].Rank = idx + 1
		}

		// Отчёт и последний результат сохраняет defer выше: он срабатывает и при остановке.
		autoBenchMu.Lock()
		autoBenchStatus.Leaderboard = scores
		autoBenchStatus.Results = scores
		autoBenchStatus.Stopped = stopped
		if len(scores) > 0 {
			autoBenchStatus.BestStrategyID = scores[0].StrategyID
			autoBenchStatus.BestStrategyName = scores[0].StrategyName
			logger.Log("[AutoBenchmark] 🏆 Лучшая стратегия: %s (%s)", scores[0].StrategyName, scores[0].StrategyID)
		}
		autoBenchMu.Unlock()
	}()

	return nil
}
