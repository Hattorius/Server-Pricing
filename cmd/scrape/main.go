package main

import (
	"encoding/json"
	"log/slog"
	"os"

	"github.com/Hattorius/Server-Pricing/internal/data"
	"github.com/Hattorius/Server-Pricing/internal/hetzner"
	hetznerauction "github.com/Hattorius/Server-Pricing/internal/hetzner_auction"
	"github.com/Hattorius/Server-Pricing/internal/netcup"
)

func main() {
	slog.Info("Scraper starting")

	scrapers := []data.Scraper{
		hetzner.Scraper{},
		hetznerauction.Scraper{},
		netcup.Scraper{},
	}

	servers := make([]data.Server, 0)
	for _, scraper := range scrapers {
		items, err := scraper.Get()
		if err != nil {
			slog.Error("Scraper failed", "provider", scraper.Name(), "error", err)
			os.Exit(1)
		}

		slog.Info("Scraper finished", "provider", scraper.Name(), "servers", len(items))
		servers = append(servers, items...)
	}

	payload, err := json.MarshalIndent(servers, "", "  ")
	if err != nil {
		slog.Error("Failed to serialize servers", "error", err)
		os.Exit(1)
	}

	err = os.WriteFile("servers.json", payload, 0o644)
	if err != nil {
		slog.Error("Failed writing output file", "error", err)
		os.Exit(1)
	}

	slog.Info("Scrape completed", "output", "servers.json", "servers", len(servers))
}
