package hetznerauction

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

type Scraper struct{}

var httpClient = &http.Client{Timeout: 15 * time.Second}

func (Scraper) Name() string {
	return "hetzner_auction"
}

func (Scraper) Get() ([]data.Server, error) {
	return Get()
}

func Get() ([]data.Server, error) {
	resp, err := httpClient.Get("https://www.hetzner.com/_resources/app/data/app/live_data_sb_EUR.json")
	if err != nil {
		slog.Error("Failed making request to Hetzner", "error", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("Hetzner returned non-OK status", "status", resp.Status)
		return nil, fmt.Errorf("hetzner auction returned non-OK status: %s", resp.Status)
	}

	var payload liveDataResponse
	err = json.NewDecoder(resp.Body).Decode(&payload)
	if err != nil {
		slog.Error("Failed decoding Hetzner response", "error", err)
		return nil, err
	}

	servers := make([]data.Server, 0, len(payload.Server))

	for i, serverRaw := range payload.Server {
		var serverObj map[string]json.RawMessage
		err = json.Unmarshal(serverRaw, &serverObj)
		if err != nil {
			slog.Error("Failed decoding server object", "index", i, "error", err)
			continue
		}

		price, err := rawFloat64(serverObj, "price")
		if err != nil {
			slog.Error("Failed decoding base price", "index", i, "error", err)
			continue
		}

		ipMonthlyPrice, err := nestedRawFloat64(serverObj, "ip_price", "Monthly")
		if err != nil {
			slog.Error("Failed decoding ip monthly price", "index", i, "error", err)
			continue
		}

		serverData := data.Server{
			Provider: "hetzner_auction",
			Link:     hetznerAuctionURL(serverObj),
			CPU:      strings.TrimSpace(rawStringOrEmpty(serverObj, "cpu")),
			CPUCores: normalize.OptionalIfNonZero(rawInt64OrZero(serverObj, "cpu_count")),
			RamSize:  rawInt64OrZero(serverObj, "ram_size"),
			Price:    price + ipMonthlyPrice,
			SetupPrice: func() float64 {
				v, err := rawFloat64(serverObj, "setup_price")
				if err != nil {
					return 0
				}
				return v
			}(),
		}

		serverData.DiskSize.HDD = nestedRawInt64SliceSum(serverObj, "serverDiskData", "hdd")
		serverData.DiskSize.SSD = nestedRawInt64SliceSum(serverObj, "serverDiskData", "sata")
		serverData.DiskSize.NVME = nestedRawInt64SliceSum(serverObj, "serverDiskData", "nvme")

		servers = append(servers, serverData)
	}

	return servers, nil
}

func hetznerAuctionURL(serverObj map[string]json.RawMessage) string {
	id := rawInt64OrZero(serverObj, "id")
	if id == 0 {
		return "https://www.hetzner.com/sb/"
	}

	return fmt.Sprintf("https://www.hetzner.com/sb/#search=%d", id)
}

func rawStringOrEmpty(obj map[string]json.RawMessage, key string) string {
	raw, ok := obj[key]
	if !ok {
		return ""
	}

	var value string
	err := json.Unmarshal(raw, &value)
	if err != nil {
		return ""
	}

	return value
}

func rawInt64OrZero(obj map[string]json.RawMessage, key string) int64 {
	raw, ok := obj[key]
	if !ok {
		return 0
	}

	var value int64
	err := json.Unmarshal(raw, &value)
	if err != nil {
		return 0
	}

	return value
}

func rawFloat64(obj map[string]json.RawMessage, key string) (float64, error) {
	raw, ok := obj[key]
	if !ok {
		return 0, fmt.Errorf("missing key %q", key)
	}

	var value float64
	err := json.Unmarshal(raw, &value)
	if err != nil {
		return 0, err
	}

	return value, nil
}

func nestedRawFloat64(obj map[string]json.RawMessage, parentKey, childKey string) (float64, error) {
	rawParent, ok := obj[parentKey]
	if !ok {
		return 0, fmt.Errorf("missing key %q", parentKey)
	}

	var parentObj map[string]json.RawMessage
	err := json.Unmarshal(rawParent, &parentObj)
	if err != nil {
		return 0, err
	}

	rawChild, ok := parentObj[childKey]
	if !ok {
		return 0, fmt.Errorf("missing key %q", childKey)
	}

	var value float64
	err = json.Unmarshal(rawChild, &value)
	if err != nil {
		return 0, err
	}

	return value, nil
}

func nestedRawInt64SliceSum(obj map[string]json.RawMessage, parentKey, childKey string) int64 {
	rawParent, ok := obj[parentKey]
	if !ok {
		return 0
	}

	var parentObj map[string]json.RawMessage
	err := json.Unmarshal(rawParent, &parentObj)
	if err != nil {
		return 0
	}

	rawChild, ok := parentObj[childKey]
	if !ok {
		return 0
	}

	var sizes []int64
	err = json.Unmarshal(rawChild, &sizes)
	if err != nil {
		return 0
	}

	var total int64
	for _, size := range sizes {
		total += size
	}

	return total
}
