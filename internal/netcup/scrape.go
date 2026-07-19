package netcup

import (
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Hattorius/Server-Pricing/internal/data"
	"github.com/Hattorius/Server-Pricing/internal/normalize"
)

const (
	listURL             = "https://www.netcup.com/en/server/root-server"
	baseURL             = "https://www.netcup.com"
	annualDiscountRatio = 0.17
	defaultCPUFrequency = 3.7
)

var (
	httpClient = &http.Client{Timeout: 20 * time.Second}

	offerPattern = regexp.MustCompile(`"RS ([0-9]+) G12","([0-9]+(?:\.[0-9]+)?\+[0-9]+(?:\.[0-9]+)?)",\[(.*?)\]`)
	ramPattern   = regexp.MustCompile(`([0-9]+) GB DDR5(?: ECC)? RAM`)
	corePattern  = regexp.MustCompile(`([0-9]+) dedicated cores`)
	corePattern2 = regexp.MustCompile(`"CPU cores":"([0-9]+) dedicated"`)
	nvmePattern  = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?) (TB|GB) NVMe`)
)

type Scraper struct{}

func (Scraper) Name() string {
	return "netcup"
}

func (Scraper) Get() ([]data.Server, error) {
	return Get()
}

func Get() ([]data.Server, error) {
	listBody, err := fetchBody(listURL)
	if err != nil {
		return nil, err
	}

	servers := parseOffers(listBody)

	if len(servers) == 0 {
		return nil, fmt.Errorf("no netcup servers could be parsed")
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
		return "", fmt.Errorf("netcup returned non-OK status for %s: %s", url, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func parseOffers(body string) []data.Server {
	matches := offerPattern.FindAllStringSubmatch(body, -1)
	servers := make([]data.Server, 0, len(matches))

	for _, match := range matches {
		if len(match) < 4 {
			continue
		}

		tier := match[1]
		priceRaw := match[2]
		productURL := fmt.Sprintf("%s/en/server/root-server/rs-%s-g12-ip-iv-12m", baseURL, tier)
		productBody, err := fetchBody(productURL)
		if err != nil {
			slog.Error("Failed fetching netcup product page", "url", productURL, "error", err)
			continue
		}

		ramSize := findInt64(ramPattern, productBody)
		cpuCores := findInt64(corePattern, contextWindow(body, match[0], 4500))
		if cpuCores == 0 {
			cpuCores = findInt64(corePattern, productBody)
		}
		if cpuCores == 0 {
			cpuCores = findInt64(corePattern2, productBody)
		}
		nvmeSize := findDiskSizeGB(nvmePattern, productBody)
		monthlyPrice, ok := normalizeMonthlyPrice(priceRaw)
		if !ok {
			continue
		}

		server := data.Server{
			Provider:     "netcup",
			Link:         productURL,
			CPU:          "AMD EPYC 9645",
			CPUCores:     normalize.OptionalIfNonZero(cpuCores),
			CPUFrequency: normalize.OptionalIfNonZero(defaultCPUFrequency),
			RamSize:      ramSize,
			Price:        monthlyPrice,
			SetupPrice:   0,
			DiskSize: data.DiskSize{
				NVME: nvmeSize,
			},
		}

		servers = append(servers, server)
	}

	return servers
}

func findFirst(re *regexp.Regexp, input string) string {
	match := re.FindStringSubmatch(input)
	if len(match) < 2 {
		return ""
	}

	return match[1]
}

func findInt64(re *regexp.Regexp, input string) int64 {
	value := findFirst(re, input)
	if value == "" {
		return 0
	}

	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}

	return n
}

func findDiskSizeGB(re *regexp.Regexp, input string) int64 {
	match := re.FindStringSubmatch(input)
	if len(match) < 3 {
		return 0
	}

	size, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0
	}

	unit := strings.ToUpper(match[2])
	if unit == "TB" {
		size *= 1024
	}

	return int64(math.Round(size))
}

func contextWindow(body, anchor string, span int) string {
	idx := strings.Index(body, anchor)
	if idx < 0 {
		return body
	}

	start := idx - span/2
	if start < 0 {
		start = 0
	}

	end := idx + span/2
	if end > len(body) {
		end = len(body)
	}

	return body[start:end]
}

func normalizeMonthlyPrice(raw string) (float64, bool) {
	parts := strings.Split(raw, "+")
	if len(parts) != 2 {
		return 0, false
	}

	discountedBase, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, false
	}

	monthlyAddon, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return 0, false
	}

	undiscountedBase := discountedBase / (1 - annualDiscountRatio)
	monthly := undiscountedBase + monthlyAddon

	return math.Round(monthly*100) / 100, true
}
