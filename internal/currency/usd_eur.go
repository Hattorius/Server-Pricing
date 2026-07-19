package currency

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"
)

const (
	fallbackUSDEURRate = 0.9
	rateURL            = "https://api.frankfurter.app/latest?from=USD&to=EUR"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

type rateResponse struct {
	Rates map[string]float64 `json:"rates"`
}

func USDEURRate() (float64, error) {
	req, err := http.NewRequest(http.MethodGet, rateURL, nil)
	if err != nil {
		return fallbackUSDEURRate, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fallbackUSDEURRate, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fallbackUSDEURRate, fmt.Errorf("failed to get usd/eur rate: %s", resp.Status)
	}

	var payload rateResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return fallbackUSDEURRate, err
	}

	rate, ok := payload.Rates["EUR"]
	if !ok || rate <= 0 {
		return fallbackUSDEURRate, fmt.Errorf("eur rate missing in response")
	}

	return rate, nil
}

func USDToEUR(usd float64, rate float64) float64 {
	return math.Round(usd*rate*100) / 100
}
