package scrapctl

import (
	"encoding/json"
	"fmt"
	"io"
)

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type DoctorReport struct {
	Status string  `json:"status"`
	Checks []Check `json:"checks"`
	Health *Health `json:"health,omitempty"`
}

func writeJSON(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	return enc.Encode(value)
}

func writeDoctorText(w io.Writer, report DoctorReport) error {
	if _, err := fmt.Fprintf(w, "status: %s\n", report.Status); err != nil {
		return fmt.Errorf("write doctor status: %w", err)
	}
	for _, check := range report.Checks {
		if check.Reason == "" {
			_, err := fmt.Fprintf(w, "%s: %s\n", check.Name, check.Status)
			if err != nil {
				return fmt.Errorf("write doctor check: %w", err)
			}
			continue
		}
		if _, err := fmt.Fprintf(w, "%s: %s (%s)\n", check.Name, check.Status, check.Reason); err != nil {
			return fmt.Errorf("write doctor check: %w", err)
		}
	}
	return nil
}

func reportFailed(checks []Check) bool {
	for _, check := range checks {
		if check.Status == "fail" {
			return true
		}
	}
	return false
}
