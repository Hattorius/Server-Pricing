package racknerd

import (
	"fmt"
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

var (
	httpClient = &http.Client{Timeout: 20 * time.Second}

	rowPattern   = regexp.MustCompile(`(?is)<tr>(.*?)</tr>`)
	tdPattern    = regexp.MustCompile(`(?is)<td[^>]*>(.*?)</td>`)
	tagPattern   = regexp.MustCompile(`(?is)<[^>]+>`)
	spacePattern = regexp.MustCompile(`\s+`)

	usdPattern   = regexp.MustCompile(`\$\s*([0-9]+(?:\.[0-9]+)?)`)
	ramPattern   = regexp.MustCompile(`(?i)([0-9]+)\s*GB\s*(?:DDR[0-9]\s*)?RAM`)
	corePattern  = regexp.MustCompile(`(?i)([0-9]+)x`)
	freqPattern  = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*GHz`)
	drivePattern = regexp.MustCompile(`(?i)^(?:(\d+)x\s*)?([0-9]+(?:\.[0-9]+)?)\s*(TB|GB)\s*(NVME|SSD|HDD)$`)
	linkPattern  = regexp.MustCompile(`href\s*=\s*"([^"]+)"`)
)

var sourceURLs = []string{
	"https://www.racknerd.com/hybrid-dedicated-servers",
	"https://www.racknerd.com/dedicated-servers",
	"https://www.racknerd.com/amd-ryzen-dedicated-servers",
}

type Scraper struct{}

func (Scraper) Name() string {
	return "racknerd"
}

func (Scraper) Get() ([]data.Server, error) {
	return Get()
}

func Get() ([]data.Server, error) {
	rate, rateErr := currency.USDEURRate()
	if rateErr != nil {
		slog.Warn("Using fallback USD/EUR rate", "error", rateErr, "rate", rate)
	}

	servers := make([]data.Server, 0)
	seenLinks := make(map[string]struct{})

	for _, sourceURL := range sourceURLs {
		body, err := fetchBody(sourceURL)
		if err != nil {
			slog.Error("Failed fetching RackNerd source page", "url", sourceURL, "error", err)
			continue
		}

		items := parseRows(body, sourceURL, rate)
		for _, srv := range items {
			if _, exists := seenLinks[srv.Link]; exists {
				continue
			}
			seenLinks[srv.Link] = struct{}{}
			servers = append(servers, srv)
		}
	}

	if len(servers) == 0 {
		return nil, fmt.Errorf("no racknerd plans parsed")
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
		return "", fmt.Errorf("racknerd returned non-OK status for %s: %s", url, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func parseRows(body, sourceURL string, usdEurRate float64) []data.Server {
	rows := rowPattern.FindAllStringSubmatch(body, -1)
	servers := make([]data.Server, 0)

	for _, row := range rows {
		if len(row) < 2 {
			continue
		}

		rawRow := row[1]
		if !strings.Contains(strings.ToLower(rawRow), "class=\"price\"") {
			continue
		}

		tdMatches := tdPattern.FindAllStringSubmatch(rawRow, -1)
		if len(tdMatches) < 4 {
			continue
		}

		cells := make([]string, 0, len(tdMatches))
		for _, td := range tdMatches {
			cells = append(cells, cleanHTML(td[1]))
		}

		cpu := normalizeCPUText(cells[0])
		storage := cells[1]
		ram := cells[2]
		if strings.Contains(sourceURL, "hybrid-dedicated-servers") && len(cells) >= 3 {
			ram = cells[1]
			storage = cells[2]
		}
		priceCell := ""
		for _, cell := range cells {
			if strings.Contains(cell, "$") {
				priceCell = cell
				break
			}
		}
		if priceCell == "" {
			continue
		}

		usdPrice, ok := parseUSD(priceCell)
		if !ok {
			continue
		}

		href := parseOrderLink(rawRow)
		if href == "" {
			href = sourceURL
		}

		srv := data.Server{
			Provider:     "racknerd",
			Link:         href,
			CPU:          cpu,
			CPUCores:     normalize.OptionalIfNonZero(parseCoreCount(cpu)),
			CPUFrequency: normalize.OptionalIfNonZero(parseCPUFrequency(cpu)),
			RamSize:      parseRAMGB(ram),
			Price:        currency.USDToEUR(usdPrice, usdEurRate),
			SetupPrice:   0,
			DiskSize:     parseStorage(storage),
		}

		servers = append(servers, srv)
	}

	return servers
}

func cleanHTML(s string) string {
	withoutTags := tagPattern.ReplaceAllString(s, " ")
	withoutEntities := strings.ReplaceAll(withoutTags, "&amp;", "&")
	withoutEntities = strings.ReplaceAll(withoutEntities, "&nbsp;", " ")
	withoutEntities = strings.ReplaceAll(withoutEntities, "™", "")
	return strings.TrimSpace(spacePattern.ReplaceAllString(withoutEntities, " "))
}

func parseUSD(s string) (float64, bool) {
	match := usdPattern.FindStringSubmatch(s)
	if len(match) < 2 {
		return 0, false
	}

	amount, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, false
	}

	return amount, true
}

func parseRAMGB(s string) int64 {
	match := ramPattern.FindStringSubmatch(s)
	if len(match) < 2 {
		return 0
	}

	n, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0
	}

	return n
}

func parseCoreCount(cpu string) int64 {
	match := corePattern.FindStringSubmatch(cpu)
	if len(match) < 2 {
		return 0
	}

	n, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0
	}

	return n
}

func parseCPUFrequency(cpu string) float64 {
	match := freqPattern.FindStringSubmatch(cpu)
	if len(match) < 2 {
		return 0
	}

	n, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0
	}

	return n
}

func parseStorage(storage string) data.DiskSize {
	result := data.DiskSize{}

	parts := strings.Split(storage, "+")
	for _, part := range parts {
		normalized := strings.TrimSpace(strings.ToUpper(part))
		normalized = spacePattern.ReplaceAllString(normalized, " ")

		match := drivePattern.FindStringSubmatch(normalized)
		if len(match) < 5 {
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

		unit := match[3]
		typeName := match[4]

		sizeGB := size
		if unit == "TB" {
			sizeGB = sizeGB * 1024
		}
		totalGB := int64(sizeGB * float64(multiplier))

		switch typeName {
		case "HDD":
			result.HDD += totalGB
		case "SSD":
			result.SSD += totalGB
		case "NVME":
			result.NVME += totalGB
		}
	}

	return result
}

func parseOrderLink(row string) string {
	match := linkPattern.FindStringSubmatch(row)
	if len(match) < 2 {
		return ""
	}

	link := strings.TrimSpace(strings.ReplaceAll(match[1], "&amp;", "&"))
	if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
		return link
	}

	if strings.HasPrefix(link, "/") {
		return "https://www.racknerd.com" + link
	}

	if link == "" {
		return ""
	}

	return "https://www.racknerd.com/" + link
}

func normalizeCPUText(raw string) string {
	input := strings.TrimSpace(raw)
	markers := []string{"Dual ", "Intel ", "AMD ", "Xeon ", "Ryzen "}

	best := -1
	for _, marker := range markers {
		idx := strings.Index(input, marker)
		if idx >= 0 && (best == -1 || idx < best) {
			best = idx
		}
	}

	if best <= 0 {
		return input
	}

	return strings.TrimSpace(input[best:])
}
