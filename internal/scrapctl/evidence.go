package scrapctl

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	evidenceMarkerHeader = "X-Scrap-Evidence-Marker"
	defaultProfile       = "heap"
	defaultProfileSecs   = 30
)

type evidenceOptions struct {
	common  commonOptions
	marker  string
	profile string
	outPath string
	seconds int
}

func runEvidence(args []string, stdout io.Writer, deps Deps) error {
	if len(args) == 0 {
		return errors.New("usage: scrapctl evidence <log-probe|pprof>")
	}
	switch args[0] {
	case "log-probe":
		return runEvidenceLogProbe(args[1:], stdout, deps)
	case "pprof":
		return runEvidencePprof(args[1:], stdout, deps)
	default:
		return fmt.Errorf("unsupported evidence command %q", args[0])
	}
}

func runEvidenceLogProbe(args []string, stdout io.Writer, deps Deps) error {
	opts, err := parseEvidenceOptions("evidence log-probe", args, func(fs *flag.FlagSet, opts *evidenceOptions) {
		fs.StringVar(&opts.marker, "marker", "", "Evidence marker to emit through the admin health endpoint")
	})
	if err != nil {
		return err
	}
	if opts.marker == "" {
		opts.marker = "scrapctl-evidence-" + time.Now().UTC().Format("20060102T150405Z")
	}
	cctx, cancel := commandContext(context.Background(), opts.common.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, strings.TrimRight(opts.common.adminURL, "/")+"/healthz", nil)
	if err != nil {
		return fmt.Errorf("build health request: %w", err)
	}
	req.Header.Set(evidenceMarkerHeader, opts.marker)
	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET healthz: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET healthz status: %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return writeByFormat(stdout, opts.common.output, operationReport{
		Status: "ok",
		Action: "evidence.log-probe",
		Marker: opts.marker,
	})
}

func runEvidencePprof(args []string, stdout io.Writer, deps Deps) error {
	opts, err := parseEvidenceOptions("evidence pprof", args, func(fs *flag.FlagSet, opts *evidenceOptions) {
		fs.StringVar(&opts.profile, "profile", defaultProfile, "pprof profile: heap, cpu, goroutine, trace, allocs, block, mutex")
		fs.StringVar(&opts.outPath, "out", "", "Output profile path")
		fs.IntVar(&opts.seconds, "seconds", defaultProfileSecs, "CPU or trace profile duration in seconds")
	})
	if err != nil {
		return err
	}
	if opts.outPath == "" {
		return errors.New("out is required")
	}
	path, err := pprofPath(opts.profile, opts.seconds)
	if err != nil {
		return err
	}
	cctx, cancel := commandContext(context.Background(), opts.common.timeout+time.Duration(opts.seconds)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, strings.TrimRight(opts.common.adminURL, "/")+path, nil)
	if err != nil {
		return fmt.Errorf("build pprof request: %w", err)
	}
	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET pprof: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET pprof status: %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read pprof: %w", err)
	}
	if err := os.WriteFile(opts.outPath, body, 0o600); err != nil {
		return fmt.Errorf("write pprof %s: %w", opts.outPath, err)
	}
	return writeByFormat(stdout, opts.common.output, operationReport{
		Status:  "ok",
		Action:  "evidence.pprof",
		Profile: opts.profile,
		Path:    opts.outPath,
	})
}

func parseEvidenceOptions(name string, args []string, configure func(*flag.FlagSet, *evidenceOptions)) (evidenceOptions, error) {
	opts := evidenceOptions{}
	fs := newFlagSet(name, &opts.common)
	if configure != nil {
		configure(fs, &opts)
	}
	if err := fs.Parse(args); err != nil {
		return evidenceOptions{}, fmt.Errorf("parse flags: %w", err)
	}
	if err := validateCommon(opts.common); err != nil {
		return evidenceOptions{}, err
	}
	return opts, nil
}

func pprofPath(profile string, seconds int) (string, error) {
	switch profile {
	case "heap", "goroutine", "allocs", "block", "mutex":
		return "/debug/pprof/" + profile, nil
	case "cpu":
		if seconds <= 0 {
			return "", errors.New("seconds must be positive")
		}
		return fmt.Sprintf("/debug/pprof/profile?seconds=%d", seconds), nil
	case "trace":
		if seconds <= 0 {
			return "", errors.New("seconds must be positive")
		}
		return fmt.Sprintf("/debug/pprof/trace?seconds=%d", seconds), nil
	default:
		return "", fmt.Errorf("unsupported profile %q", profile)
	}
}
