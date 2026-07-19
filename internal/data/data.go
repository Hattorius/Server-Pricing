package data

type DiskSize struct {
	HDD  int64 `json:"hdd"`
	SSD  int64 `json:"ssd"`
	NVME int64 `json:"nvme"`
}

type Server struct {
	Provider     string   `json:"provider"`
	Link         string   `json:"link"`
	CPU          string   `json:"cpu"`
	CPUCores     *int64   `json:"cpuCores,omitempty"`
	CPUThreads   *int64   `json:"cpuThreads,omitempty"`
	CPUBenchmark *int64   `json:"cpuBenchmark,omitempty"`
	CPUFrequency *float64 `json:"cpuFrequencyGHz,omitempty"`
	RamSize      int64    `json:"ramSizeGB"`
	Price        float64  `json:"priceMonthlyEUR"`
	SetupPrice   float64  `json:"setupPriceEUR"`
	DiskSize     DiskSize `json:"diskSizeGB"`
}
