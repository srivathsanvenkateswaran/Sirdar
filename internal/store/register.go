package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// RegisterRow is one line of the append-only run register.
type RegisterRow struct {
	Key, Kind, RunID, Date               string
	Provider, Model, Service             string
	Classification, Confidence, Severity string
	Turns                                int
	CostUSD                              float64
	TriageVerdict, NotePath              string
}

func registerPath(root string) string {
	return filepath.Join(root, ".sirdar", "register.jsonl")
}

// AppendRegister appends row as one JSON line to <root>/.sirdar/register.jsonl.
func AppendRegister(root string, row RegisterRow) error {
	if err := os.MkdirAll(filepath.Join(root, ".sirdar"), 0o755); err != nil {
		return fmt.Errorf("store: create .sirdar dir: %w", err)
	}
	data, err := json.Marshal(row)
	if err != nil {
		return fmt.Errorf("store: marshal register row: %w", err)
	}
	f, err := os.OpenFile(registerPath(root), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("store: open register: %w", err)
	}
	defer f.Close()
	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("store: write register row: %w", err)
	}
	return nil
}

// ReadRegister reads all rows from <root>/.sirdar/register.jsonl in file
// order. It returns an empty slice, not an error, if the register does not
// exist yet.
func ReadRegister(root string) ([]RegisterRow, error) {
	f, err := os.Open(registerPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: open register: %w", err)
	}
	defer f.Close()

	var rows []RegisterRow
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var row RegisterRow
		if err := json.Unmarshal(line, &row); err != nil {
			return nil, fmt.Errorf("store: unmarshal register row: %w", err)
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("store: scan register: %w", err)
	}
	return rows, nil
}
