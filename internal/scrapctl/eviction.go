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
	"net/url"
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

type evictionApplyOptions struct {
	common  commonOptions
	planID  string
	confirm bool
}

type evictionStatusOptions struct {
	common commonOptions
	planID string
}

func runEviction(args []string, stdout io.Writer, deps Deps) error {
	if len(args) == 0 {
		return errors.New("usage: scrapctl eviction <plan|apply|status>")
	}
	switch args[0] {
	case "plan":
		return runEvictionPlan(args[1:], stdout, deps)
	case "apply":
		return runEvictionApply(args[1:], stdout, deps)
	case "status":
		return runEvictionStatus(args[1:], stdout, deps)
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

func runEvictionApply(args []string, stdout io.Writer, deps Deps) error {
	opts, err := parseEvictionApplyOptions(args)
	if err != nil {
		return err
	}

	ctx, cancel := commandContext(context.Background(), opts.common.timeout)
	defer cancel()
	result, err := postEvictionApply(ctx, opts.common, deps, opts.planID)
	if err != nil {
		return err
	}
	result = eviction.OperatorSafeApplyResult(result)
	if opts.common.output == "json" {
		if err := writeJSON(stdout, result); err != nil {
			return err
		}
		return failedEvictionApplyResultError(result)
	}
	if err := writeEvictionApplyText(stdout, result); err != nil {
		return err
	}
	return failedEvictionApplyResultError(result)
}

func runEvictionStatus(args []string, stdout io.Writer, deps Deps) error {
	opts, err := parseEvictionStatusOptions(args)
	if err != nil {
		return err
	}

	ctx, cancel := commandContext(context.Background(), opts.common.timeout)
	defer cancel()
	status, err := getEvictionStatus(ctx, opts.common, deps, opts.planID)
	if err != nil {
		return err
	}
	status = eviction.OperatorSafePlanStatus(status)
	if opts.common.output == "json" {
		return writeJSON(stdout, status)
	}
	return writeEvictionStatusText(stdout, status)
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

func parseEvictionApplyOptions(args []string) (evictionApplyOptions, error) {
	opts := evictionApplyOptions{}
	fs := newFlagSet("eviction apply", &opts.common, func(fs *flag.FlagSet, _ *commonOptions) {
		fs.StringVar(&opts.planID, "plan-id", "", "Stored eviction plan ID")
		fs.BoolVar(&opts.confirm, "confirm", false, "Confirm applying the stored eviction plan")
	})
	if err := fs.Parse(args); err != nil {
		return evictionApplyOptions{}, fmt.Errorf("parse flags: %w", err)
	}
	if err := validateCommon(opts.common); err != nil {
		return evictionApplyOptions{}, err
	}
	if strings.TrimSpace(opts.planID) == "" {
		return evictionApplyOptions{}, errors.New("plan-id is required")
	}
	if !opts.confirm {
		return evictionApplyOptions{}, errors.New("confirm is required")
	}
	return opts, nil
}

func parseEvictionStatusOptions(args []string) (evictionStatusOptions, error) {
	opts := evictionStatusOptions{}
	fs := newFlagSet("eviction status", &opts.common, func(fs *flag.FlagSet, _ *commonOptions) {
		fs.StringVar(&opts.planID, "plan-id", "", "Stored eviction plan ID")
	})
	if err := fs.Parse(args); err != nil {
		return evictionStatusOptions{}, fmt.Errorf("parse flags: %w", err)
	}
	if err := validateCommon(opts.common); err != nil {
		return evictionStatusOptions{}, err
	}
	if strings.TrimSpace(opts.planID) == "" {
		return evictionStatusOptions{}, errors.New("plan-id is required")
	}
	return opts, nil
}

func postEvictionPlan(ctx context.Context, opts commonOptions, deps Deps, planReq eviction.PlanRequest) (eviction.Plan, error) {
	deps, err := withHTTPClientTLS(deps, opts, opts.adminURL)
	if err != nil {
		return eviction.Plan{}, err
	}
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
		return eviction.Plan{}, fmt.Errorf("POST eviction plan status: %d: %s", resp.StatusCode, eviction.OperatorSafeErrorText(strings.TrimSpace(string(data))))
	}

	var plan eviction.Plan
	if err := json.NewDecoder(resp.Body).Decode(&plan); err != nil {
		return eviction.Plan{}, fmt.Errorf("decode eviction plan: %w", err)
	}
	return plan, nil
}

func postEvictionApply(ctx context.Context, opts commonOptions, deps Deps, planID string) (eviction.ApplyResult, error) {
	deps, err := withHTTPClientTLS(deps, opts, opts.adminURL)
	if err != nil {
		return eviction.ApplyResult{}, err
	}
	endpoint := strings.TrimRight(opts.adminURL, "/") + "/admin/eviction/plans/" + url.PathEscape(planID) + "/apply"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader("{}"))
	if err != nil {
		return eviction.ApplyResult{}, fmt.Errorf("build eviction apply request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return eviction.ApplyResult{}, fmt.Errorf("POST eviction apply: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return eviction.ApplyResult{}, fmt.Errorf("POST eviction apply status: %d: %s", resp.StatusCode, eviction.OperatorSafeErrorText(strings.TrimSpace(string(data))))
	}

	var result eviction.ApplyResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return eviction.ApplyResult{}, fmt.Errorf("decode eviction apply: %w", err)
	}
	return result, nil
}

func getEvictionStatus(ctx context.Context, opts commonOptions, deps Deps, planID string) (eviction.PlanStatus, error) {
	deps, err := withHTTPClientTLS(deps, opts, opts.adminURL)
	if err != nil {
		return eviction.PlanStatus{}, err
	}
	endpoint := strings.TrimRight(opts.adminURL, "/") + "/admin/eviction/plans/" + url.PathEscape(planID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return eviction.PlanStatus{}, fmt.Errorf("build eviction status request: %w", err)
	}

	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return eviction.PlanStatus{}, fmt.Errorf("GET eviction status: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return eviction.PlanStatus{}, fmt.Errorf("GET eviction status: %d: %s", resp.StatusCode, eviction.OperatorSafeErrorText(strings.TrimSpace(string(data))))
	}

	var status eviction.PlanStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return eviction.PlanStatus{}, fmt.Errorf("decode eviction status: %w", err)
	}
	return status, nil
}

func failedEvictionApplyResultError(result eviction.ApplyResult) error {
	switch result.Status {
	case eviction.ApplyStatusFailed:
		return fmt.Errorf("eviction apply failed: plan_id=%s failed_blocks=%d", result.PlanID, result.FailedBlocks)
	case eviction.ApplyStatusEvictedWithValidationFailure:
		return fmt.Errorf("eviction apply validation failed: plan_id=%s validation_failed_blocks=%d", result.PlanID, result.ValidationFailedBlocks)
	default:
		return nil
	}
}

func writeEvictionStatusText(w io.Writer, status eviction.PlanStatus) error {
	if _, err := fmt.Fprintf(w, "plan_id: %s\nstatus: %s\n", status.PlanID, status.Status); err != nil {
		return fmt.Errorf("write status summary: %w", err)
	}
	if status.Plan != nil {
		if err := writeEvictionPlanEvidenceText(w, *status.Plan); err != nil {
			return err
		}
	}
	if status.ApplyResult != nil {
		if _, err := fmt.Fprintln(w, "apply_result:"); err != nil {
			return fmt.Errorf("write apply result label: %w", err)
		}
		if err := writeEvictionApplyText(w, *status.ApplyResult); err != nil {
			return err
		}
	}
	return nil
}

func writeEvictionPlanEvidenceText(w io.Writer, plan eviction.Plan) error {
	if _, err := fmt.Fprintf(w, "member_hostname: %s\nmember_id: %s\n", plan.MemberHostname, plan.MemberID); err != nil {
		return fmt.Errorf("write status member identity: %w", err)
	}
	if _, err := fmt.Fprintf(w, "reason: %s\n", plan.Reason); err != nil {
		return fmt.Errorf("write status reason: %w", err)
	}
	if _, err := fmt.Fprintf(
		w,
		"candidate_blocks: %d\ncandidate_bytes: %d\neligible_blocks: %d\neligible_bytes: %d\nselected_bytes: %d\n",
		plan.CandidateBlocks,
		plan.CandidateBytes,
		plan.EligibleBlocks,
		plan.EligibleBytes,
		plan.SelectedBytes,
	); err != nil {
		return fmt.Errorf("write candidate evidence: %w", err)
	}
	if err := writeEvictionSkipCounts(w, plan.SkipCountsByReason); err != nil {
		return err
	}
	if err := writeEvictionBlocks(w, "selected_blocks", plan.Selected); err != nil {
		return err
	}
	return writeEvictionBlocks(w, "skipped_candidates", plan.Skipped)
}

func writeEvictionSkipCounts(w io.Writer, counts map[string]int) error {
	if _, err := fmt.Fprintln(w, "skip_counts_by_reason:"); err != nil {
		return fmt.Errorf("write skip count label: %w", err)
	}
	for reason, count := range counts {
		if _, err := fmt.Fprintf(w, "  %s=%d\n", reason, count); err != nil {
			return fmt.Errorf("write skip count: %w", err)
		}
	}
	return nil
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

func writeEvictionApplyText(w io.Writer, result eviction.ApplyResult) error {
	if _, err := fmt.Fprintf(w, "plan_id: %s\nstatus: %s\n", result.PlanID, result.Status); err != nil {
		return fmt.Errorf("write apply summary: %w", err)
	}
	if _, err := fmt.Fprintf(
		w,
		"selected_blocks: %d\nevicted_blocks: %d\nskipped_blocks: %d\nfailed_blocks: %d\nvalidated_blocks: %d\nvalidation_failed_blocks: %d\nbytes_freed: %d\n",
		result.SelectedBlocks,
		result.EvictedBlocks,
		result.SkippedBlocks,
		result.FailedBlocks,
		result.ValidatedBlocks,
		result.ValidationFailedBlocks,
		result.BytesFreed,
	); err != nil {
		return fmt.Errorf("write apply totals: %w", err)
	}
	if _, err := fmt.Fprintln(w, "blocks:"); err != nil {
		return fmt.Errorf("write apply blocks label: %w", err)
	}
	for _, block := range result.Blocks {
		if err := writeEvictionApplyBlock(w, block); err != nil {
			return err
		}
	}
	if len(result.Validations) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "validations:"); err != nil {
		return fmt.Errorf("write validations label: %w", err)
	}
	for _, validation := range result.Validations {
		if err := writeEvictionValidationBlock(w, validation); err != nil {
			return err
		}
	}
	return nil
}

func writeEvictionApplyBlock(w io.Writer, block eviction.ApplyBlock) error {
	if _, err := fmt.Fprintf(
		w,
		"  block_id=%d shard_id=%d size_bytes=%d status=%s bytes_freed=%d",
		block.BlockID,
		block.ShardID,
		block.SizeBytes,
		block.Status,
		block.BytesFreed,
	); err != nil {
		return fmt.Errorf("write apply block: %w", err)
	}
	if block.Reason != "" {
		if _, err := fmt.Fprintf(w, " reason=%s", block.Reason); err != nil {
			return fmt.Errorf("write apply reason: %w", err)
		}
	}
	if block.Error != "" {
		if _, err := fmt.Fprintf(w, " error=%q", block.Error); err != nil {
			return fmt.Errorf("write apply error: %w", err)
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return fmt.Errorf("write apply newline: %w", err)
	}
	return nil
}

func writeEvictionValidationBlock(w io.Writer, validation eviction.ValidationBlock) error {
	if _, err := fmt.Fprintf(
		w,
		"  block_id=%d shard_id=%d status=%s",
		validation.BlockID,
		validation.ShardID,
		validation.Status,
	); err != nil {
		return fmt.Errorf("write validation block: %w", err)
	}
	if validation.Error != "" {
		if _, err := fmt.Fprintf(w, " error=%q", validation.Error); err != nil {
			return fmt.Errorf("write validation error: %w", err)
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return fmt.Errorf("write validation newline: %w", err)
	}
	return nil
}

func writeEvictionBlocks(w io.Writer, label string, blocks []eviction.PlanBlock) error {
	if _, err := fmt.Fprintf(w, "%s:\n", label); err != nil {
		return fmt.Errorf("write %s label: %w", label, err)
	}
	for _, block := range blocks {
		if _, err := fmt.Fprintf(
			w,
			"  block_id=%d shard_id=%d size_bytes=%d confirmed_at_us=%d restored_at_us=%d eligible_at_us=%d hot_residency_window_seconds=%d local_state=%s open_readers=%d repair_state=%s",
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
