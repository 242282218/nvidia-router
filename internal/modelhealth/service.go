package modelhealth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/modelcatalog"
)

var errAllKeysCooling = errors.New("all NVIDIA keys are cooling")

type ModelSource interface {
	List(context.Context) ([]modelcatalog.Model, error)
}

type ModelProber interface {
	TestModel(context.Context, string, int64, int64) error
}

type KeySource interface {
	FirstEnabledID(context.Context) (int64, error)
	CountEnabled(context.Context) (int, error)
}

type Service struct {
	repository *Repository
	catalog    ModelSource
	prober     ModelProber
	keys       KeySource
	clock      clock.Clock
	logger     *slog.Logger
	trigger    chan struct{}
	wake       chan struct{}
	startOnce  sync.Once
	done       chan struct{}
}

func NewService(repository *Repository, catalog ModelSource, prober ModelProber, keys KeySource, source clock.Clock, logger *slog.Logger) *Service {
	if source == nil {
		source = clock.RealClock{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		repository: repository, catalog: catalog, prober: prober, keys: keys,
		clock: source, logger: logger, trigger: make(chan struct{}, 1), wake: make(chan struct{}, 1),
	}
}

type Summary struct {
	Range        string         `json:"range"`
	From         time.Time      `json:"from"`
	To           time.Time      `json:"to"`
	Models       []ModelSummary `json:"models"`
	TotalModels  int            `json:"total_models"`
	Healthy      int            `json:"healthy_count"`
	Degraded     int            `json:"degraded_count"`
	Unavailable  int            `json:"unavailable_count"`
	Unchecked    int            `json:"unchecked_count"`
	Stale        int            `json:"stale_count"`
	Unconfigured int            `json:"unconfigured_count"`
	Settings     Settings       `json:"settings"`
}

type ModelSummary struct {
	ModelID             int64      `json:"model_id"`
	PublicID            string     `json:"public_id"`
	DisplayName         string     `json:"display_name"`
	Kind                string     `json:"kind"`
	Provider            string     `json:"provider"`
	Enabled             bool       `json:"enabled"`
	Status              Status     `json:"status"`
	SuccessRate         float64    `json:"success_rate"`
	ProbeCount          int64      `json:"probe_count"`
	SuccessCount        int64      `json:"success_count"`
	FailureCount        int64      `json:"failure_count"`
	TimeoutCount        int64      `json:"timeout_count"`
	SkippedCount        int64      `json:"skipped_count"`
	LastProbeAt         *time.Time `json:"last_probe_at,omitempty"`
	LastDurationMS      *int64     `json:"last_duration_ms,omitempty"`
	LastErrorCode       string     `json:"last_error_code,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	Buckets             []Bucket   `json:"buckets"`
}

type rangeSpec struct {
	duration time.Duration
	buckets  int
}

func (s *Service) Settings(ctx context.Context) (Settings, error) {
	return s.repository.LoadSettings(ctx)
}

func (s *Service) UpdateSettings(ctx context.Context, settings Settings) (Settings, error) {
	return s.PatchSettings(ctx, SettingsPatch{
		Enabled:         boolValue(settings.Enabled),
		IntervalSeconds: intValue(settings.IntervalSeconds),
		Concurrency:     intValue(settings.Concurrency),
	})
}

func (s *Service) PatchSettings(ctx context.Context, patch SettingsPatch) (Settings, error) {
	updated, err := s.repository.PatchSettings(ctx, patch, s.clock.Now())
	if err != nil {
		return Settings{}, err
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	if updated.Enabled {
		// Enabling the feature should produce a first status snapshot promptly;
		// the configured interval controls subsequent runs.
		s.RunNow()
	}
	return updated, nil
}

func (s *Service) Summary(ctx context.Context, rangeName, sortName string) (Summary, error) {
	spec, err := rangeSpecFor(rangeName)
	if err != nil {
		return Summary{}, err
	}
	now := s.clock.Now().UTC()
	from := now.Add(-spec.duration)
	models, err := s.catalog.List(ctx)
	if err != nil {
		return Summary{}, fmt.Errorf("list models for health summary: %w", err)
	}
	snapshot, err := s.repository.SummarySnapshot(ctx, from, now)
	if err != nil {
		return Summary{}, err
	}
	byModel := make(map[int64][]ProbeEvent, len(models))
	for _, event := range snapshot.Events {
		byModel[event.ModelID] = append(byModel[event.ModelID], event)
	}
	items := make([]ModelSummary, 0, len(models))
	for _, model := range models {
		provider := model.Provider
		if provider == "" {
			provider = modelcatalog.ProviderNVIDIA
		}
		modelEvents := byModel[model.ID]
		stats := summarizeEvents(modelEvents)
		latestValue, hasLatest := snapshot.Latest[model.ID]
		var latestPointer *Latest
		var lastProbeAt *time.Time
		var lastDuration *int64
		lastErrorCode := ""
		consecutiveFailures := 0
		if hasLatest {
			latestCopy := latestValue
			latestPointer = &latestCopy
			lastProbe := latestValue.LastProbeAt
			lastProbeAt = &lastProbe
			lastDurationValue := latestValue.DurationMS
			lastDuration = &lastDurationValue
			lastErrorCode = latestValue.ErrorCode
			consecutiveFailures = latestValue.ConsecutiveFailures
		}
		status := ClassifyStatus(latestPointer, stats.WindowStats, now, time.Duration(snapshot.Settings.IntervalSeconds)*time.Second)
		item := ModelSummary{
			ModelID: model.ID, PublicID: model.PublicID, DisplayName: model.DisplayName,
			Kind: string(model.Kind), Provider: provider, Enabled: model.Enabled,
			Status: status, SuccessRate: stats.SuccessRate, ProbeCount: stats.ProbeCount,
			SuccessCount: stats.SuccessCount, FailureCount: stats.FailureCount,
			TimeoutCount: stats.TimeoutCount, SkippedCount: stats.SkippedCount,
			LastProbeAt: lastProbeAt, LastDurationMS: lastDuration,
			LastErrorCode: lastErrorCode, ConsecutiveFailures: consecutiveFailures,
			Buckets: BuildBuckets(from, now, spec.buckets, modelEvents),
		}
		items = append(items, item)
	}
	sortModelSummaries(items, sortName)
	result := Summary{Range: rangeName, From: from, To: now, Models: items, TotalModels: len(items), Settings: snapshot.Settings}
	for _, item := range items {
		switch item.Status {
		case StatusHealthy:
			result.Healthy++
		case StatusDegraded:
			result.Degraded++
		case StatusUnavailable:
			result.Unavailable++
		case StatusStale:
			result.Stale++
		case StatusUnconfigured:
			result.Unconfigured++
		default:
			result.Unchecked++
		}
	}
	return result, nil
}

type eventStats struct {
	WindowStats
	SkippedCount int64
	SuccessRate  float64
}

func summarizeEvents(events []ProbeEvent) eventStats {
	var stats eventStats
	for _, event := range events {
		stats.ProbeCount++
		switch event.Outcome {
		case OutcomeSuccess:
			stats.SuccessCount++
		case OutcomeFailure:
			stats.FailureCount++
		case OutcomeTimeout:
			stats.TimeoutCount++
		case OutcomeSkipped:
			stats.SkippedCount++
		}
	}
	attempts := stats.SuccessCount + stats.FailureCount + stats.TimeoutCount
	if attempts > 0 {
		stats.SuccessRate = float64(stats.SuccessCount) * 100 / float64(attempts)
	}
	return stats
}

func sortModelSummaries(items []ModelSummary, sortName string) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		switch sortName {
		case "quality":
			if left.SuccessRate != right.SuccessRate {
				return left.SuccessRate > right.SuccessRate
			}
			if left.ProbeCount != right.ProbeCount {
				return left.ProbeCount > right.ProbeCount
			}
		case "availability":
			if statusRank(left.Status) != statusRank(right.Status) {
				return statusRank(left.Status) < statusRank(right.Status)
			}
			if left.SuccessRate != right.SuccessRate {
				return left.SuccessRate > right.SuccessRate
			}
		case "latency":
			leftMs := int64(1<<62 - 1)
			if left.LastDurationMS != nil {
				leftMs = *left.LastDurationMS
			}
			rightMs := int64(1<<62 - 1)
			if right.LastDurationMS != nil {
				rightMs = *right.LastDurationMS
			}
			if leftMs != rightMs {
				return leftMs < rightMs
			}
		case "volume":
			if left.ProbeCount != right.ProbeCount {
				return left.ProbeCount > right.ProbeCount
			}
		case "name":
			if left.DisplayName != right.DisplayName {
				return left.DisplayName < right.DisplayName
			}
		case "recent":
			if compareTimes(left.LastProbeAt, right.LastProbeAt) != 0 {
				return compareTimes(left.LastProbeAt, right.LastProbeAt) > 0
			}
		}
		return left.PublicID < right.PublicID
	})
}

func statusRank(status Status) int {
	switch status {
	case StatusHealthy:
		return 0
	case StatusDegraded:
		return 1
	case StatusStale:
		return 2
	case StatusUnavailable:
		return 3
	case StatusUnchecked:
		return 4
	case StatusUnconfigured:
		return 5
	default:
		return 6
	}
}

func compareTimes(left, right *time.Time) int {
	if left == nil && right == nil {
		return 0
	}
	if left == nil {
		return -1
	}
	if right == nil {
		return 1
	}
	if left.Before(*right) {
		return -1
	}
	if left.After(*right) {
		return 1
	}
	return 0
}

func (s *Service) RunOnce(ctx context.Context) error {
	settings, err := s.repository.LoadSettings(ctx)
	if err != nil {
		return err
	}
	models, err := s.catalog.List(ctx)
	if err != nil {
		return fmt.Errorf("list models for health probe: %w", err)
	}
	keyID, keyErr := s.selectNVIDIAKey(ctx, models)
	concurrency := settings.Concurrency
	if concurrency < MinConcurrency || concurrency > MaxConcurrency {
		concurrency = DefaultConcurrency
	}
	jobs := make(chan modelcatalog.Model)
	var workers sync.WaitGroup
	var errorsMu sync.Mutex
	var runErrors []error
	for index := 0; index < concurrency; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for model := range jobs {
				if err := s.probeOne(ctx, model, keyID, keyErr); err != nil {
					errorsMu.Lock()
					runErrors = append(runErrors, err)
					errorsMu.Unlock()
				}
			}
		}()
	}
	for _, model := range models {
		select {
		case jobs <- model:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return ctx.Err()
		}
	}
	close(jobs)
	workers.Wait()
	s.pruneProbeHistory(ctx)
	return errors.Join(runErrors...)
}

// pruneProbeHistory drops probe rows past the readable window. It runs once per
// cycle rather than per probe, and a failure only leaves history in place, so it
// never fails the run that produced fresh data.
func (s *Service) pruneProbeHistory(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	deleted, err := s.repository.DeleteProbesBefore(ctx, s.clock.Now().UTC().Add(-probeRetention))
	if err != nil {
		s.logger.Warn("model health probe cleanup failed", "error", err)
		return
	}
	if deleted > 0 {
		s.logger.Info("model health probe cleanup completed", "deleted", deleted)
	}
}

func (s *Service) selectNVIDIAKey(ctx context.Context, models []modelcatalog.Model) (int64, error) {
	for _, model := range models {
		provider := model.Provider
		if provider == "" {
			provider = modelcatalog.ProviderNVIDIA
		}
		if provider != modelcatalog.ProviderNVIDIA {
			continue
		}
		if s.keys == nil {
			return 0, errors.New("NVIDIA key source is unavailable")
		}
		id, err := s.keys.FirstEnabledID(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			// No keys available right now — distinguish "no keys at all" from
			// "keys exist but all are cooling". When a 429 storm puts every key
			// into cooldown, FirstEnabledID returns sql.ErrNoRows because its
			// WHERE clause filters out cooling keys. Without the distinction,
			// probeOne writes outcome=skipped + errorCode="no_credential", and
			// ClassifyStatus returns StatusUnconfigured, which tells the ops
			// dashboard "you haven't configured any keys" instead of "your keys
			// are rate-limited and cooling down". The two states require
			// completely different operator actions.
			count, countErr := s.keys.CountEnabled(ctx)
			if countErr != nil {
				// Count failed; can't distinguish, treat as generic no-key
				return 0, err
			}
			if count > 0 {
				// Keys exist but all are cooling
				return 0, errAllKeysCooling
			}
			// Truly no keys configured
			return 0, err
		}
		if err != nil {
			return 0, err
		}
		return id, nil
	}
	return 0, nil
}

func (s *Service) probeOne(ctx context.Context, model modelcatalog.Model, keyID int64, keyErr error) error {
	provider := model.Provider
	if provider == "" {
		provider = modelcatalog.ProviderNVIDIA
	}
	started := time.Now()
	var outcome string
	errorCode := ""
	if provider == modelcatalog.ProviderNVIDIA && keyID <= 0 {
		outcome = OutcomeSkipped
		if errors.Is(keyErr, errAllKeysCooling) {
			errorCode = "keys_cooling"
		} else if errors.Is(keyErr, sql.ErrNoRows) || keyErr == nil {
			errorCode = "no_credential"
		} else {
			errorCode = "key_source_unavailable"
		}
	} else {
		probeKeyID := keyID
		if provider != modelcatalog.ProviderNVIDIA {
			probeKeyID = 0
		}
		err := s.prober.TestModel(ctx, provider, probeKeyID, model.ID)
		outcome, errorCode = classifyModelProbeError(err)
	}
	if ctx.Err() != nil {
		outcome = OutcomeCanceled
		errorCode = "canceled"
	}
	duration := time.Since(started).Milliseconds()
	if duration < 0 {
		duration = 0
	}
	recordCtx := ctx
	if outcome == OutcomeCanceled {
		var cancel context.CancelFunc
		recordCtx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
	}
	if err := s.repository.Record(recordCtx, ProbeEvent{
		ModelID: model.ID, Outcome: outcome, DurationMS: duration,
		ErrorCode: errorCode, CreatedAt: s.clock.Now(),
	}); err != nil {
		return fmt.Errorf("record model %q health probe: %w", model.PublicID, err)
	}
	return nil
}

func boolValue(value bool) *bool { return &value }

func intValue(value int) *int { return &value }

func classifyModelProbeError(err error) (string, string) {
	if err == nil {
		return OutcomeSuccess, ""
	}
	switch {
	case errors.Is(err, modelcatalog.ErrProviderNotConfigured):
		return OutcomeSkipped, "provider_not_configured"
	case errors.Is(err, modelcatalog.ErrNVIDIAKeyRequired):
		return OutcomeSkipped, "no_credential"
	case errors.Is(err, modelcatalog.ErrProviderNotRoutable):
		return OutcomeSkipped, "provider_not_routable"
	case errors.Is(err, modelcatalog.ErrModelNotFound):
		return OutcomeFailure, "model_not_found"
	default:
		return ClassifyProbeError(err), SafeErrorCode(err)
	}
}

func rangeSpecFor(name string) (rangeSpec, error) {
	switch name {
	case "1h":
		return rangeSpec{duration: time.Hour, buckets: 60}, nil
	case "6h":
		return rangeSpec{duration: 6 * time.Hour, buckets: 60}, nil
	case "24h":
		return rangeSpec{duration: 24 * time.Hour, buckets: 60}, nil
	case "7d":
		return rangeSpec{duration: 7 * 24 * time.Hour, buckets: 60}, nil
	default:
		return rangeSpec{}, fmt.Errorf("model health range %q is invalid", name)
	}
}

func (s *Service) RunNow() {
	select {
	case s.trigger <- struct{}{}:
	default:
	}
}

func (s *Service) Start(ctx context.Context) <-chan struct{} {
	s.startOnce.Do(func() {
		s.done = make(chan struct{})
		go func() {
			defer close(s.done)
			s.loop(ctx)
		}()
	})
	return s.done
}

func (s *Service) loop(ctx context.Context) {
	settings, err := s.repository.LoadSettings(ctx)
	if err != nil {
		s.logger.Error("load model health settings", "error", err)
		settings = DefaultSettings()
	}
	if settings.Enabled {
		s.RunNow()
	}
	timer := time.NewTimer(nextInterval(settings))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.trigger:
			if err := s.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				s.logger.Warn("model health probe batch failed", "error", err)
			}
			resetTimer(timer, nextInterval(settings))
		case <-s.wake:
			settings = s.reloadSettings(ctx, settings)
			resetTimer(timer, nextInterval(settings))
		case <-timer.C:
			settings = s.reloadSettings(ctx, settings)
			if settings.Enabled {
				if err := s.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
					s.logger.Warn("scheduled model health probe failed", "error", err)
				}
			}
			timer.Reset(nextInterval(settings))
		}
	}
}

func (s *Service) reloadSettings(ctx context.Context, fallback Settings) Settings {
	settings, err := s.repository.LoadSettings(ctx)
	if err != nil {
		s.logger.Warn("reload model health settings failed", "error", err)
		return fallback
	}
	return settings
}

func nextInterval(settings Settings) time.Duration {
	if settings.IntervalSeconds < MinIntervalSeconds {
		return time.Duration(DefaultIntervalSeconds) * time.Second
	}
	return time.Duration(settings.IntervalSeconds) * time.Second
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}
