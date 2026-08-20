package dashboard

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hwennnn/radar/internal/pipeline"
)

func loadDiscoverySeedFile(path string) (pipeline.DiscoverySeed, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return pipeline.DiscoverySeed{}, fmt.Errorf("open discovery seed: %w", err)
	}
	defer file.Close()
	return pipeline.LoadDiscoverySeed(file)
}

func loadCatalogFile(path string) (pipeline.Catalog, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return pipeline.Catalog{}, fmt.Errorf("open verified catalog: %w", err)
	}
	defer file.Close()
	return pipeline.LoadCatalog(file)
}
