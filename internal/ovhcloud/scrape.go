package ovhcloud

import (
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Hattorius/Server-Pricing/internal/currency"
	"github.com/Hattorius/Server-Pricing/internal/data"
	"github.com/Hattorius/Server-Pricing/internal/normalize"
)

const (
	pricesURL = "https://www.ovhcloud.com/en/bare-metal/prices/"
	baseURL   = "https://www.ovhcloud.com"
)

var (
	httpClient = &http.Client{Timeout: 25 * time.Second}

	cardPattern = regexp.MustCompile(`(?is)<div class="ods-card--all-server"[^>]*data-product="true"[^>]*data-product-id="([^"]+)"[^>]*>(.*?)<div class="ods-card--all-server__compare`)

	stripTagPattern = regexp.MustCompile(`(?is)<[^>]+>`)
	spacePattern    = regexp.MustCompile(`\s+`)

	namePattern      = regexp.MustCompile(`(?is)<div\s+class="otds-text"[^>]*>\s*([A-Z0-9-]+)\s*</div>`)
	cpuSpecPattern   = regexp.MustCompile(`(?is)spec spec--cpu.*?<span class="spec-tech">(.*?)</span>\s*</strong>`)
	cpuScorePattern  = regexp.MustCompile(`(?is)spec spec--cpuScore.*?<strong>\s*([0-9]+)\s*</strong>`)
	ramSpecPattern   = regexp.MustCompile(`(?is)spec spec--memory.*?<span class="value font-weight-bold">(.*?)</span>`)
	storeSpecPattern = regexp.MustCompile(`(?is)spec spec--storage.*?<strong>(.*?)</strong>`)
	ramCellPattern   = regexp.MustCompile(`(?is)<span class="visually-hidden">RAM</span>.*?<span class="value font-weight-bold">(.*?)</span>`)
	storeCellPattern = regexp.MustCompile(`(?is)<span class="visually-hidden">Storage</span>.*?<span class="value font-weight-bold">(.*?)</span>`)
	pricePattern     = regexp.MustCompile(`(?is)class="price-value">\s*\$?\s*([0-9]+(?:\.[0-9]+)?)`)
	linkPattern      = regexp.MustCompile(`(?is)<a[^>]*href="([^"]+)"`)

	coresThreadsPattern = regexp.MustCompile(`(?i)([0-9]+)\s*c\s*/\s*([0-9]+)\s*t`)
	freqPattern         = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*GHz`)
	modelPattern        = regexp.MustCompile(`(?i)(AMD|INTEL)[A-Z0-9\-+ ]+`)
	ramPattern          = regexp.MustCompile(`(?i)([0-9]+)\s*GB`)
	storageQtyPattern   = regexp.MustCompile(`(?i)(?:(\d+)\s*x\s*)?([0-9]+(?:\.[0-9]+)?)\s*(TB|GB)`)
)

type Scraper struct{}

func (Scraper) Name() string {
	return "ovhcloud"
}

func (Scraper) Get() ([]data.Server, error) {
	return Get()
}

func Get() ([]data.Server, error) {
	rate, rateErr := currency.USDEURRate()
	if rateErr != nil {
		slog.Warn("Using fallback USD/EUR rate for OVHcloud", "error", rateErr, "rate", rate)
	}

	body, err := fetchBody(pricesURL)
	if err != nil {
		return nil, err
	}

	matches := cardPattern.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no ovhcloud server cards found")
	}

	servers := make([]data.Server, 0, len(matches))
	seen := make(map[string]struct{})
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}

		productID := strings.TrimSpace(match[1])
		cardHTML := match[2]
		server, ok := parseCard(productID, cardHTML, rate)
		if !ok {
			continue
		}

		if _, exists := seen[server.Link]; exists {
			continue
		}
		seen[server.Link] = struct{}{}
		servers = append(servers, server)
	}

	if len(servers) == 0 {
		return nil, fmt.Errorf("no ovhcloud plans could be parsed")
	}

	return servers, nil
}

func fetchBody(url string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Server-Pricing-Bot/1.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ovhcloud returned non-OK status: %s", resp.Status)
	}

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(payload), nil
}

func parseCard(productID, cardHTML string, usdEurRate float64) (data.Server, bool) {
	priceUSD, ok := parseUSDPrice(cardHTML)
	if !ok {
		return data.Server{}, false
	}

	name := parseName(cardHTML)
	cpuSpec := parseCPUSpec(cardHTML)
	cpu := parseCPUModel(cpuSpec)
	if cpu == "" {
		cpu = name
	}

	cpuCores, cpuThreads := parseCoresThreads(cpuSpec, cardHTML)
	cpuFreq := parseFrequency(cpuSpec, cardHTML)
	cpuBenchmark := parseCPUBenchmark(cardHTML)

	ramSize := parseRAM(cardHTML)
	storageValue := parseStorageValue(cardHTML)
	disk := parseStorage(storageValue, cardHTML)

	link := parseLink(cardHTML)
	if link == "" {
		link = pricesURL + "#" + sanitizeProductID(productID)
	}

	return data.Server{
		Provider:     "ovhcloud",
		Link:         link,
		CPU:          cpu,
		CPUCores:     normalize.OptionalIfNonZero(cpuCores),
		CPUThreads:   normalize.OptionalIfNonZero(cpuThreads),
		CPUBenchmark: normalize.OptionalIfNonZero(cpuBenchmark),
		CPUFrequency: normalize.OptionalIfNonZero(cpuFreq),
		RamSize:      ramSize,
		Price:        currency.USDToEUR(priceUSD, usdEurRate),
		SetupPrice:   0,
		DiskSize:     disk,
	}, true
}

func parseUSDPrice(cardHTML string) (float64, bool) {
	match := pricePattern.FindStringSubmatch(cardHTML)
	if len(match) < 2 {
		return 0, false
	}

	value, err := strconv.ParseFloat(strings.TrimSpace(match[1]), 64)
	if err != nil {
		return 0, false
	}

	return value, true
}

func parseName(cardHTML string) string {
	match := namePattern.FindStringSubmatch(cardHTML)
	if len(match) < 2 {
		return ""
	}

	return cleanText(match[1])
}

func parseCPUSpec(cardHTML string) string {
	match := cpuSpecPattern.FindStringSubmatch(cardHTML)
	if len(match) < 2 {
		return ""
	}

	return cleanText(match[1])
}

func parseCPUModel(cpuSpec string) string {
	prefix := cpuSpec
	if idx := coresThreadsPattern.FindStringIndex(cpuSpec); idx != nil && idx[0] > 0 {
		prefix = cpuSpec[:idx[0]]
	}

	match := modelPattern.FindString(prefix)
	if match == "" {
		return cleanText(prefix)
	}

	return cleanText(match)
}

func parseCoresThreads(cpuSpec, cardHTML string) (int64, int64) {
	match := coresThreadsPattern.FindStringSubmatch(cpuSpec)
	if len(match) < 3 {
		match = coresThreadsPattern.FindStringSubmatch(cleanText(cardHTML))
	}
	if len(match) < 3 {
		return 0, 0
	}

	cores, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		cores = 0
	}
	threads, err := strconv.ParseInt(match[2], 10, 64)
	if err != nil {
		threads = 0
	}

	return cores, threads
}

func parseFrequency(cpuSpec, cardHTML string) float64 {
	match := freqPattern.FindStringSubmatch(cpuSpec)
	if len(match) < 2 {
		match = freqPattern.FindStringSubmatch(cleanText(cardHTML))
	}
	if len(match) < 2 {
		return 0
	}

	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0
	}

	return value
}

func parseCPUBenchmark(cardHTML string) int64 {
	match := cpuScorePattern.FindStringSubmatch(cardHTML)
	if len(match) < 2 {
		return 0
	}

	value, err := strconv.ParseInt(strings.TrimSpace(match[1]), 10, 64)
	if err != nil {
		return 0
	}

	return value
}

func parseRAM(cardHTML string) int64 {
	raw := ""
	match := ramCellPattern.FindStringSubmatch(cardHTML)
	if len(match) >= 2 {
		raw = cleanText(match[1])
	} else {
		match = ramSpecPattern.FindStringSubmatch(cardHTML)
	}
	if len(match) >= 2 {
		raw = cleanText(match[1])
	} else {
		raw = cleanText(cardHTML)
	}

	ramMatch := ramPattern.FindStringSubmatch(raw)
	if len(ramMatch) < 2 {
		return 0
	}

	value, err := strconv.ParseInt(ramMatch[1], 10, 64)
	if err != nil {
		return 0
	}

	return value
}

func parseStorageValue(cardHTML string) string {
	match := storeCellPattern.FindStringSubmatch(cardHTML)
	if len(match) >= 2 {
		return cleanText(match[1])
	}

	match = storeSpecPattern.FindStringSubmatch(cardHTML)
	if len(match) < 2 {
		return ""
	}

	return cleanText(match[1])
}

func parseStorage(storageValue, cardHTML string) data.DiskSize {
	result := data.DiskSize{}
	if storageValue == "" {
		return result
	}

	typeHint := storageTypeHint(cardHTML)
	parts := strings.Split(storageValue, "+")
	for _, part := range parts {
		p := cleanText(part)
		match := storageQtyPattern.FindStringSubmatch(strings.ToUpper(p))
		if len(match) < 4 {
			continue
		}

		multiplier := int64(1)
		if match[1] != "" {
			m, err := strconv.ParseInt(match[1], 10, 64)
			if err == nil && m > 0 {
				multiplier = m
			}
		}

		size, err := strconv.ParseFloat(match[2], 64)
		if err != nil {
			continue
		}
		unit := strings.ToUpper(match[3])
		if unit == "TB" {
			size *= 1024
		}

		total := int64(size * float64(multiplier))
		switch typeHint {
		case "HDD":
			result.HDD += total
		case "SSD":
			result.SSD += total
		default:
			result.NVME += total
		}
	}

	return result
}

func storageTypeHint(cardHTML string) string {
	upper := strings.ToUpper(cleanText(cardHTML))
	switch {
	case strings.Contains(upper, "NVME"):
		return "NVME"
	case strings.Contains(upper, "HDD"):
		return "HDD"
	case strings.Contains(upper, "SSD"):
		return "SSD"
	default:
		return "NVME"
	}
}

func parseLink(cardHTML string) string {
	matches := linkPattern.FindAllStringSubmatch(cardHTML, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}

		link := html.UnescapeString(strings.TrimSpace(match[1]))
		if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
			return link
		}
		if strings.HasPrefix(link, "/") {
			return baseURL + link
		}
	}

	return ""
}

func cleanText(input string) string {
	if input == "" {
		return ""
	}

	text := stripTagPattern.ReplaceAllString(input, " ")
	text = html.UnescapeString(text)
	text = strings.ReplaceAll(text, "\u00a0", " ")
	text = strings.ReplaceAll(text, "\x00", "")
	text = strings.ReplaceAll(text, "\x1f", "")
	text = spacePattern.ReplaceAllString(text, " ")

	return strings.TrimSpace(text)
}

func sanitizeProductID(id string) string {
	value := strings.TrimSpace(strings.ToLower(id))
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.ReplaceAll(value, "|", "-")
	value = strings.ReplaceAll(value, "--", "-")
	return value
}
