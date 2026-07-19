package hetzner

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Hattorius/Server-Pricing/internal/data"
	"github.com/Hattorius/Server-Pricing/internal/normalize"
)

type liveDataResponse struct {
	Server []json.RawMessage `json:"server"`
}

type serverEntry struct {
	Product struct {
		Link string `json:"link"`
	} `json:"product"`
	Variations []variation `json:"variations"`
	PriceData  struct {
		Price      float64 `json:"price"`
		SetupPrice float64 `json:"setupPrice"`
		IPPrice    struct {
			Monthly float64 `json:"Monthly"`
		} `json:"ipPrice"`
	} `json:"priceData"`
	CPUData struct {
		CPU          string  `json:"cpu"`
		Cores        int64   `json:"cores"`
		Threads      int64   `json:"threads"`
		CPUBenchmark int64   `json:"cpuBenchmark"`
		Frequency    float64 `json:"frequency"`
	} `json:"cpuData"`
	FilterData struct {
		RamMin int64 `json:"ramMin"`
	} `json:"filterData"`
}

type variation struct {
	RAM   []ramItem   `json:"ram"`
	Drive []driveItem `json:"drive"`
}

type ramItem struct {
	RealSize int64 `json:"RealSize"`
	Amount   int64 `json:"Amount"`
}

type driveItem struct {
	RealSize int64  `json:"RealSize"`
	Amount   int64  `json:"Amount"`
	Type     string `json:"Type"`
}

type Scraper struct{}

var httpClient = &http.Client{Timeout: 15 * time.Second}

func (Scraper) Name() string {
	return "hetzner"
}

func (Scraper) Get() ([]data.Server, error) {
	return Get()
}

func Get() ([]data.Server, error) {
	resp, err := httpClient.Get("https://www.hetzner.com/_resources/app/data/app/live_data_en_EUR.json")
	if err != nil {
		slog.Error("Failed making request to Hetzner", "error", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("Hetzner returned non-OK status", "status", resp.Status)
		return nil, fmt.Errorf("hetzner returned non-OK status: %s", resp.Status)
	}

	var payload liveDataResponse
	err = json.NewDecoder(resp.Body).Decode(&payload)
	if err != nil {
		slog.Error("Failed decoding Hetzner response", "error", err)
		return nil, err
	}

	servers := make([]data.Server, 0, len(payload.Server))

	for i, serverRaw := range payload.Server {
		var entry serverEntry
		err := json.Unmarshal(serverRaw, &entry)
		if err != nil {
			slog.Error("Failed decoding normal server object", "index", i, "error", err)
			continue
		}

		priceWithIP := entry.PriceData.Price + entry.PriceData.IPPrice.Monthly
		link := entry.Product.Link
		if link == "" {
			link = "https://www.hetzner.com/dedicated-rootserver/"
		}

		if len(entry.Variations) == 0 {
			servers = append(servers, data.Server{
				Provider:     "hetzner",
				Link:         link,
				CPU:          strings.TrimSpace(entry.CPUData.CPU),
				CPUCores:     normalize.OptionalIfNonZero(entry.CPUData.Cores),
				CPUThreads:   normalize.OptionalIfNonZero(entry.CPUData.Threads),
				CPUBenchmark: normalize.OptionalIfNonZero(entry.CPUData.CPUBenchmark),
				CPUFrequency: normalize.OptionalIfNonZero(entry.CPUData.Frequency),
				RamSize:      entry.FilterData.RamMin,
				Price:        priceWithIP,
				SetupPrice:   entry.PriceData.SetupPrice,
			})
			continue
		}

		for _, variant := range entry.Variations {
			srv := data.Server{
				Provider:     "hetzner",
				Link:         link,
				CPU:          strings.TrimSpace(entry.CPUData.CPU),
				CPUCores:     normalize.OptionalIfNonZero(entry.CPUData.Cores),
				CPUThreads:   normalize.OptionalIfNonZero(entry.CPUData.Threads),
				CPUBenchmark: normalize.OptionalIfNonZero(entry.CPUData.CPUBenchmark),
				CPUFrequency: normalize.OptionalIfNonZero(entry.CPUData.Frequency),
				RamSize:      ramSizeFromVariation(variant.RAM),
				Price:        priceWithIP,
				SetupPrice:   entry.PriceData.SetupPrice,
			}

			if srv.RamSize == 0 {
				srv.RamSize = entry.FilterData.RamMin
			}

			addDriveSizes(&srv, variant.Drive)
			servers = append(servers, srv)
		}
	}

	return servers, nil
}

func ramSizeFromVariation(ram []ramItem) int64 {
	var total int64
	for _, item := range ram {
		amount := item.Amount
		if amount <= 0 {
			amount = 1
		}
		total += item.RealSize * amount
	}

	return total
}

func addDriveSizes(srv *data.Server, drives []driveItem) {
	for _, drive := range drives {
		amount := drive.Amount
		if amount <= 0 {
			amount = 1
		}

		totalSize := drive.RealSize * amount
		driveType := strings.ToUpper(strings.TrimSpace(drive.Type))

		switch {
		case strings.Contains(driveType, "NVME"):
			srv.DiskSize.NVME += totalSize
		case strings.Contains(driveType, "HDD"):
			srv.DiskSize.HDD += totalSize
		case strings.Contains(driveType, "SSD"), strings.Contains(driveType, "SATA"):
			srv.DiskSize.SSD += totalSize
		}
	}
}
