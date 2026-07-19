package data

// Scraper provides normalized server inventory from a single provider.
type Scraper interface {
	Name() string
	Get() ([]Server, error)
}
