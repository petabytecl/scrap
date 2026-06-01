package scrapctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/petabytecl/scrap/internal/eviction"
)

type evictionPlanOptions struct {
	common         commonOptions
	memberHostname string
	shardID        uint64
	maxBlocks      int
	maxBytes       int64
	reason         string
	note           string
	shardIDSet     bool
	maxBlocksSet   bool
	maxBytesSet    bool
}

func runEviction(args []string, stdout io.Writer, deps Deps) error {
	if len(args) == 0 {
		return errors.New("usage: scrapctl eviction <plan>")
	}
	switch args[0] {
	case "plan":
		return runEvictionPlan(args[1:], stdout, deps)
	default:
		return fmt.Errorf("unsupported eviction command %q", args[0])
	}
}

func runEvictionPlan(args []string, stdout io.Writer, deps Deps) error {
	opts, err := parseEvictionPlanOptions(args)
	if err != nil {
		return err
	}
	req := eviction.PlanRequest{
		MemberHostname: opts.memberHostname,
		Reason:         opts.reason,
		Note:           opts.note,
	}
	if opts.shardIDSet {
		req.ShardID = &opts.shardID
	}
	if opts.maxBlocksSet {
		req.MaxBlocks = &opts.maxBlocks
	}
	if opts.maxBytesSet {
		req.MaxBytes = &opts.maxBytes
	}

	ctx, cancel := commandContext(context.Background(), opts.common.timeout)
	defer cancel()
	plan, err := postEvictionPlan(ctx, opts.common, deps, req)
	if err != nil {
		return err
	}
	if opts.common.output == "json" {
		return writeJSON(stdout, plan)
	}
	return writeEvictionPlanText(stdout, plan)
}

func parseEvictionPlanOptions(args []string) (evictionPlanOptions, error) {
	opts := evictionPlanOptions{}
	fs := newFlagSet("eviction plan", &opts.common, func(fs *flag.FlagSet, _ *commonOptions) {
		fs.StringVar(&opts.memberHostname, "member-hostname", "", "Target Member hostname")
		fs.Uint64Var(&opts.shardID, "shard-id", 0, "Optional Shard filter")
		fs.IntVar(&opts.maxBlocks, "max-blocks", 0, "Optional max selected Blocks cap")
		fs.Int64Var(&opts.maxBytes, "max-bytes", 0, "Optional max selected bytes cap")
		fs.StringVar(&opts.reason, "reason", "", "Low-cardinality eviction reason")
		fs.StringVar(&opts.note, "note", "", "Optional audit note")
	})
	if err := fs.Parse(args); err != nil {
		return evictionPlanOptions{}, fmt.Errorf("parse flags: %w", err)
	}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "shard-id":
			opts.shardIDSet = true
		case "max-blocks":
			opts.maxBlocksSet = true
		case "max-bytes":
			opts.maxBytesSet = true
		}
	})
	if err := validateCommon(opts.common); err != nil {
		return evictionPlanOptions{}, err
	}
	if strings.TrimSpace(opts.memberHostname) == "" {
		return evictionPlanOptions{}, errors.New("member-hostname is required")
	}
	return opts, nil
}

func postEvictionPlan(ctx context.Context, opts commonOptions, deps Deps, planReq eviction.PlanRequest) (eviction.Plan, error) {
	body, err := json.Marshal(planReq)
	if err != nil {
		return eviction.Plan{}, fmt.Errorf("marshal eviction plan request: %w", err)
	}
	url := strings.TrimRight(opts.adminURL, "/") + "/admin/eviction/plans"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return eviction.Plan{}, fmt.Errorf("build eviction plan request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return eviction.Plan{}, fmt.Errorf("POST eviction plan: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return eviction.Plan{}, fmt.Errorf("POST eviction plan status: %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var plan eviction.Plan
	if err := json.NewDecoder(resp.Body).Decode(&plan); err != nil {
		return eviction.Plan{}, fmt.Errorf("decode eviction plan: %w", err)
	}
	return plan, nil
}

func writeEvictionPlanText(w io.Writer, plan eviction.Plan) error {
	if _, err := fmt.Fprintf(w, "plan_id: %s\n", plan.PlanID); err != nil {
		return fmt.Errorf("write plan id: %w", err)
	}
	if _, err := fmt.Fprintf(w, "member_hostname: %s\nmember_id: %s\n", plan.MemberHostname, plan.MemberID); err != nil {
		return fmt.Errorf("write member identity: %w", err)
	}
	if _, err := fmt.Fprintf(w, "generated_at_us: %d\nexpires_at_us: %d\n", plan.GeneratedAtUs, plan.ExpiresAtUs); err != nil {
		return fmt.Errorf("write plan times: %w", err)
	}
	if _, err := fmt.Fprintf(w, "recommended_bounds: max_blocks=%d max_bytes=%d\n", plan.RecommendedBounds.MaxBlocks, plan.RecommendedBounds.MaxBytes); err != nil {
		return fmt.Errorf("write recommended bounds: %w", err)
	}
	if _, err := fmt.Fprintf(w, "effective_bounds: max_blocks=%d max_bytes=%d\n", plan.EffectiveBounds.MaxBlocks, plan.EffectiveBounds.MaxBytes); err != nil {
		return fmt.Errorf("write effective bounds: %w", err)
	}
	if _, err := fmt.Fprintf(
		w,
		"config: enabled=%t hot_residency_window_seconds=%d plan_ttl_seconds=%d recommended_max_blocks=%d recommended_max_bytes=%d max_validate_samples=%d\n",
		plan.Config.Enabled,
		plan.Config.HotResidencyWindowSeconds,
		plan.Config.PlanTTLSeconds,
		plan.Config.RecommendedMaxBlocks,
		plan.Config.RecommendedMaxBytes,
		plan.Config.MaxValidateSamples,
	); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := writeEvictionBlocks(w, "selected_blocks", plan.Selected); err != nil {
		return err
	}
	return writeEvictionBlocks(w, "skipped_candidates", plan.Skipped)
}

func writeEvictionBlocks(w io.Writer, label string, blocks []eviction.PlanBlock) error {
	if _, err := fmt.Fprintf(w, "%s:\n", label); err != nil {
		return fmt.Errorf("write %s label: %w", label, err)
	}
	for _, block := range blocks {
		if _, err := fmt.Fprintf(
			w,
			"  block_id=%d shard_id=%d size_bytes=%d confirmed_at_us=%d restored_at_us=%d eligible_at_us=%d hot_residency_window_seconds=%d local_state=%s open_readers=%d repair_state=%s backend_key=%s",
			block.BlockID,
			block.ShardID,
			block.SizeBytes,
			block.ConfirmedAtUs,
			block.RestoredAtUs,
			block.EligibleAtUs,
			block.HotResidencyWindowSeconds,
			block.LocalState,
			block.OpenReaders,
			block.RepairState,
			block.BackendKey,
		); err != nil {
			return fmt.Errorf("write %s block: %w", label, err)
		}
		if block.Reason != "" {
			if _, err := fmt.Fprintf(w, " reason=%s", block.Reason); err != nil {
				return fmt.Errorf("write %s reason: %w", label, err)
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return fmt.Errorf("write %s newline: %w", label, err)
		}
	}
	return nil
}
