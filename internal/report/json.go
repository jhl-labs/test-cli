package report

import (
	"encoding/json"

	"github.com/jhl-labs/test-cli/internal/model"
)

func writeJSON(r *model.Report, path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, string(data)+"\n")
}

// Marshal returns the report as indented JSON bytes (used for stdout --format
// json and tests).
func Marshal(r *model.Report) ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}
