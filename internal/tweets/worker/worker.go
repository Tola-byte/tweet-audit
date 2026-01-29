package worker

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"tweet-audit/internal/config"
	"tweet-audit/internal/tweets/logger"
	"tweet-audit/internal/tweets/model"
	"tweet-audit/internal/tweets/parser"
	"tweet-audit/internal/tweets/repository"
	"tweet-audit/internal/tweets/util"
)

const (
	DailyQuotaLimit = 10000000
	TweetBatchSize  = 50
)

// QueuedTweet represents a tweet that needs Gemini scoring, with its job ID
type QueuedTweet struct {
	JobID string
	Tweet *model.TweetRecord
}

type Worker struct {
	jobQueue        chan string       // Job IDs to process
	tweetQueue      chan *QueuedTweet // Tweets needing Gemini scoring
	jobs            sync.Map          // In-memory job cache (also persisted to DB)
	stop            chan struct{}
	fileStore       model.FileStore
	repo            *repository.Repository
	parser          *parser.Parser
	scorer          model.Scorer
	policy          *Policy
	currentJobID    string // Current job being processed
	currentJobIDMu  sync.Mutex
	jobUpdateQueue  map[string]*model.Job // Batched job updates (jobID -> job)
	jobUpdateMu     sync.Mutex            // Protects jobUpdateQueue
	dailyQuotaLimit int
	tweetBatchSize  int
	activeWork      sync.WaitGroup // Tracks in-flight work
	shutdownOnce    sync.Once      // Ensures shutdown only happens once
}

type Policy struct {
	BlockedPhrases []string
	Criteria       *model.ModerationCriteria // Moderation criteria for Gemini
}

func NewWorker(fs model.FileStore, repo *repository.Repository, scorer model.Scorer) *Worker {
	cfg := config.WorkerConfig{
		JobQueueSize:      100,
		TweetQueueSize:    1000,
		DailyQuotaLimit:   DailyQuotaLimit,
		TweetBatchSize:    TweetBatchSize,
		JobUpdateInterval: 5 * time.Second,
	}
	return NewWorkerWithConfig(fs, repo, scorer, cfg)
}

func NewWorkerWithConfig(fs model.FileStore, repo *repository.Repository, scorer model.Scorer, cfg config.WorkerConfig) *Worker {
	w := &Worker{
		jobQueue:        make(chan string, cfg.JobQueueSize),
		tweetQueue:      make(chan *QueuedTweet, cfg.TweetQueueSize),
		stop:            make(chan struct{}),
		fileStore:       fs,
		repo:            repo,
		parser:          parser.NewParser(),
		scorer:          scorer,
		policy:          nil,
		dailyQuotaLimit: cfg.DailyQuotaLimit,
		tweetBatchSize:  cfg.TweetBatchSize,
	}

	if scorer != nil {
		criteria := model.DefaultCriteria()
		scorer.SetCriteria(criteria)
		logger.Info("Moderation criteria configured: %d forbidden words, professional_check=%v, exclude_politics=%v",
			len(criteria.ForbiddenWords), criteria.ProfessionalCheck, criteria.ExcludePolitics)
	}

	w.jobUpdateQueue = make(map[string]*model.Job)

	go w.jobLoop()
	go w.tweetLoop()
	go w.jobUpdateFlusherWithInterval(cfg.JobUpdateInterval)
	w.resumePendingJobs()
	return w
}

// resumePendingJobs loads unprocessed tweets from running jobs into the queue
func (w *Worker) resumePendingJobs() {
	if w.repo == nil {
		return
	}

	// This is a simplified version - in production, you'd query for running jobs
	// For now, we'll handle it when jobs are processed
	logger.Info("Worker started - ready to process jobs")
}

func (w *Worker) EnqueueParse(fileID string, criteria *model.ModerationCriteria) string {
	id := util.GenID()
	job := &model.Job{
		ID:             id,
		FileID:         fileID,
		Status:         model.JobQueued,
		ProcessedCount: 0,
		TotalCount:     0,
		GeminiCalls:    0,
		FlaggedCount:   0,
		Criteria:       criteria,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if w.repo != nil {
		if err := w.repo.SaveJob(job); err != nil {
			logger.Error("Failed to save job to DB: %v", err)
		}
	}

	w.jobs.Store(id, job)
	w.jobQueue <- id

	logger.WithFields(map[string]interface{}{
		"job_id":  id,
		"file_id": fileID,
		"status":  "queued",
	}).Info("Job enqueued for processing")

	return id
}

func (w *Worker) GetJob(id string) (*model.Job, bool) {
	if v, ok := w.jobs.Load(id); ok {
		return v.(*model.Job), true
	}

	if w.repo != nil {
		job, err := w.repo.GetJob(id)
		if err == nil && job != nil {
			w.jobs.Store(id, job)
			return job, true
		}
	}

	return nil, false
}

// jobLoop processes job queue: parse archive, apply filters, queue tweets for Gemini
func (w *Worker) jobLoop() {
	defer w.activeWork.Done()
	w.activeWork.Add(1)

	for {
		select {
		case id, ok := <-w.jobQueue:
			if !ok {
				logger.Info("Job queue closed, stopping job worker")
				return
			}
			w.processJob(context.Background(), id)
		case <-w.stop:
			logger.Info("Job worker received stop signal, draining queue...")
			w.drainJobQueue()
			logger.Info("Job worker stopped")
			return
		}
	}
}

// tweetLoop processes tweet queue: Gemini scoring with rate limiting and quota tracking
func (w *Worker) tweetLoop() {
	defer w.activeWork.Done()
	w.activeWork.Add(1)

	for {
		select {
		case tweet, ok := <-w.tweetQueue:
			if !ok {
				logger.Info("Tweet queue closed, stopping tweet worker")
				return
			}
			w.processTweetWithGemini(context.Background(), tweet)
		case <-w.stop:
			logger.Info("Tweet worker received stop signal, draining queue...")
			w.drainTweetQueue()
			logger.Info("Tweet worker stopped")
			return
		}
	}
}

// drainJobQueue processes remaining jobs in queue after stop signal
func (w *Worker) drainJobQueue() {
	for {
		select {
		case id := <-w.jobQueue:
			logger.Info("Draining job from queue: %s", id)
			w.processJob(context.Background(), id)
		default:
			return
		}
	}
}

// drainTweetQueue processes remaining tweets in queue after stop signal
func (w *Worker) drainTweetQueue() {
	for {
		select {
		case tweet := <-w.tweetQueue:
			logger.Info("Draining tweet from queue: %s", tweet.Tweet.ID)
			w.processTweetWithGemini(context.Background(), tweet)
		default:
			return
		}
	}
}

// Stop signals workers to stop accepting new work and begin shutdown
func (w *Worker) Stop() {
	w.shutdownOnce.Do(func() {
		logger.Info("Stopping worker (signaling stop)...")
		close(w.stop)
	})
}

// Shutdown gracefully shuts down the worker, waiting for in-flight work to complete
// Returns error if shutdown exceeds the provided timeout
func (w *Worker) Shutdown(ctx context.Context) error {
	w.Stop()

	logger.Info("Waiting for workers to finish in-flight work...")

	done := make(chan struct{})
	go func() {
		w.activeWork.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("All workers finished, flushing final job updates...")
		w.flushJobUpdates()
		logger.Info("Worker shutdown complete")
		return nil
	case <-ctx.Done():
		logger.Warn("Worker shutdown timeout exceeded, forcing shutdown")
		return fmt.Errorf("worker shutdown timeout: %w", ctx.Err())
	}
}

// processJob handles the full job processing pipeline
func (w *Worker) processJob(ctx context.Context, jobID string) {
	w.activeWork.Add(1)
	defer w.activeWork.Done()

	w.currentJobIDMu.Lock()
	w.currentJobID = jobID
	w.currentJobIDMu.Unlock()
	defer func() {
		w.currentJobIDMu.Lock()
		w.currentJobID = ""
		w.currentJobIDMu.Unlock()
	}()

	job, err := w.repo.GetJob(jobID)
	if err != nil || job == nil {
		logger.Warn("Job not found: %s", jobID)
		return
	}

	logCtx := logger.WithFields(map[string]interface{}{
		"job_id":  job.ID,
		"file_id": job.FileID,
	})

	job.Status = model.JobRunning
	job.UpdatedAt = time.Now()
	w.updateJob(job)

	logCtx.Info("Starting job processing")

	tweets, err := w.parseAndSaveTweets(ctx, job)
	if err != nil {
		logCtx.Error("Failed to parse archive: %v", err)
		job.Status = model.JobFailed
		job.Error = err.Error()
		job.UpdatedAt = time.Now()
		w.updateJob(job)
		return
	}

	job.TotalCount = len(tweets)
	w.updateJob(job)

	logCtx.Info("Parsed and saved %d tweets to database", len(tweets))

	needsGeminiCount, flaggedCount, err := w.applyFiltersAndQueue(ctx, job, tweets)
	if err != nil {
		logCtx.Error("Failed to apply filters: %v", err)
		job.Status = model.JobFailed
		job.Error = err.Error()
		job.UpdatedAt = time.Now()
		w.updateJob(job)
		return
	}

	logCtx.Info("Filters applied: %d need Gemini, %d flagged immediately", needsGeminiCount, flaggedCount)
}

// parseAndSaveTweets: Phase 1 - Parse archive and save all tweets to DB
func (w *Worker) parseAndSaveTweets(ctx context.Context, job *model.Job) ([]*model.TweetRecord, error) {
	logCtx := logger.WithFields(map[string]interface{}{
		"job_id":  job.ID,
		"file_id": job.FileID,
	})

	if w.fileStore == nil || w.parser == nil {
		return nil, fmt.Errorf("missing dependencies")
	}

	logCtx.Info("Opening archive file")
	f, err := w.fileStore.Open(ctx, job.FileID)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	logCtx.Info("Reading archive into memory")
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("failed to read archive: %w", err)
	}

	sizeMB := float64(len(data)) / (1024 * 1024)
	logCtx.WithFields(map[string]interface{}{
		"size_mb": fmt.Sprintf("%.2f", sizeMB),
	}).Info("Archive loaded into memory")

	readerAt := &readerAtWrapper{data: data}

	logCtx.Info("Parsing archive (retweets will be filtered)")
	tweets, err := w.parser.ParseArchive(readerAt, int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse archive: %w", err)
	}

	// Save all tweets to DB
	logCtx.Info("Saving %d tweets to database", len(tweets))
	for _, tweet := range tweets {
		if err := w.repo.SaveTweet(job.ID, tweet); err != nil {
			logCtx.Warn("Failed to save tweet %s: %v", tweet.ID, err)
		}
	}

	return tweets, nil
}

// applyFiltersAndQueue: Phase 2 - Apply filters, flag obvious ones, queue rest for Gemini
func (w *Worker) applyFiltersAndQueue(ctx context.Context, job *model.Job, tweets []*model.TweetRecord) (int, int, error) {
	logCtx := logger.WithFields(map[string]interface{}{
		"job_id": job.ID,
	})

	needsGeminiCount := 0
	flaggedCount := 0

	logCtx.Info("Applying deterministic filters")
	for _, tweet := range tweets {
		filterResult := w.applyFilters(tweet)

		if filterResult.ShouldFlag {
			// Flagged by deterministic filter - save immediately
			flaggedCount++
			ft := &model.FlaggedTweet{
				ID:           util.GenID(),
				TweetID:      tweet.ID,
				JobID:        job.ID,
				Text:         tweet.Text,
				CreatedAt:    tweet.CreatedAt,
				FlaggedAt:    time.Now(),
				Reason:       filterResult.Reason,
				Score:        filterResult.Score,
				URL:          util.GetTweetURL(tweet.ID),
				FilterReason: filterResult.FilterReason,
			}
			if err := w.repo.SaveFlaggedTweet(ft); err != nil {
				logCtx.Warn("Failed to save flagged tweet: %v", err)
			}
			// Mark as processed (no Gemini needed)
			w.repo.MarkTweetProcessed(job.ID, tweet.ID)
		} else if w.scorer != nil {
			// Needs Gemini scoring - mark and queue
			if err := w.repo.MarkTweetNeedsGemini(job.ID, tweet.ID); err != nil {
				logCtx.Warn("Failed to mark tweet for Gemini: %v", err)
				continue
			}
			needsGeminiCount++
			// Queue for Gemini processing
			select {
			case w.tweetQueue <- &QueuedTweet{JobID: job.ID, Tweet: tweet}:
			case <-ctx.Done():
				return needsGeminiCount, flaggedCount, ctx.Err()
			}
		} else {
			// No scorer available, mark as processed
			w.repo.MarkTweetProcessed(job.ID, tweet.ID)
		}

		job.ProcessedCount++
		job.FlaggedCount = flaggedCount
		if job.ProcessedCount%100 == 0 {
			w.updateJob(job)
		}
	}

	job.FlaggedCount = flaggedCount
	w.updateJob(job)

	return needsGeminiCount, flaggedCount, nil
}

// processTweetWithGemini: Phase 3 - Process tweets from queue with Gemini (rate limited, quota tracked)
func (w *Worker) processTweetWithGemini(ctx context.Context, queued *QueuedTweet) {
	w.activeWork.Add(1)
	defer w.activeWork.Done()

	jobID := queued.JobID
	tweet := queued.Tweet

	if jobID == "" {
		logger.Warn("No job ID for tweet %s", tweet.ID)
		return
	}

	// Check daily quota
	quotaUsed, err := w.repo.GetTodayQuotaUsage()
	if err != nil {
		logger.Warn("Failed to check quota: %v", err)
	} else if quotaUsed >= w.dailyQuotaLimit {
		logger.Warn("Daily quota reached (%d/%d), pausing job", quotaUsed, w.dailyQuotaLimit)
		job, _ := w.repo.GetJob(jobID)
		if job != nil {
			job.Status = model.JobPausedQuota
			job.UpdatedAt = time.Now()
			w.updateJob(job)
		}
		return
	}

	// Get job to retrieve its criteria
	job, _ := w.repo.GetJob(jobID)
	if job == nil {
		logger.Warn("Job not found: %s", jobID)
		return
	}

	// Apply job-specific criteria if provided, otherwise use default
	if job.Criteria != nil {
		w.scorer.SetCriteria(job.Criteria)
	} else {
		// Use default criteria if job has none
		w.scorer.SetCriteria(model.DefaultCriteria())
	}

	// Score with Gemini
	scoreResult, err := w.scorer.Score(ctx, tweet)
	if err != nil {
		logger.WithFields(map[string]interface{}{
			"tweet_id": tweet.ID,
			"job_id":   jobID,
		}).Warn("Gemini scoring failed: %v", err)
		// Mark as processed anyway (failed, but we tried)
		w.repo.MarkTweetProcessed(jobID, tweet.ID)
		return
	}

	// Increment quota usage
	w.repo.IncrementQuotaUsage()

	// Update job (job already loaded and validated above)
	job.GeminiCalls++
	job.ProcessedCount++
	if scoreResult.ShouldFlag {
		job.FlaggedCount++
		// Save flagged tweet
		ft := &model.FlaggedTweet{
			ID:           util.GenID(),
			TweetID:      tweet.ID,
			JobID:        jobID,
			Text:         tweet.Text,
			CreatedAt:    tweet.CreatedAt,
			FlaggedAt:    time.Now(),
			Reason:       scoreResult.Reason,
			Score:        scoreResult.Score,
			URL:          util.GetTweetURL(tweet.ID),
			FilterReason: "llm_scorer",
		}
		w.repo.SaveFlaggedTweet(ft)
	}
	w.updateJob(job)

	// Check if job is complete
	if job.ProcessedCount >= job.TotalCount {
		job.Status = model.JobDone
		job.UpdatedAt = time.Now()
		w.updateJob(job)
		logger.WithFields(map[string]interface{}{
			"job_id": jobID,
		}).Info("Job completed: %d flagged out of %d tweets", job.FlaggedCount, job.TotalCount)
	}

	// Mark tweet as processed
	w.repo.MarkTweetProcessed(jobID, tweet.ID)
}

func (w *Worker) updateJob(job *model.Job) {
	job.UpdatedAt = time.Now()

	// Update in-memory cache immediately
	w.jobs.Store(job.ID, job)

	// Queue for batched DB write (reduces lock contention)
	if w.repo != nil {
		w.jobUpdateMu.Lock()
		w.jobUpdateQueue[job.ID] = job
		w.jobUpdateMu.Unlock()
	}
}

// jobUpdateFlusher periodically flushes job updates to database
// Runs every 2 seconds to reduce write frequency
func (w *Worker) jobUpdateFlusher() {
	w.jobUpdateFlusherWithInterval(2 * time.Second)
}

// jobUpdateFlusherWithInterval periodically flushes job updates to database with configurable interval
func (w *Worker) jobUpdateFlusherWithInterval(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.flushJobUpdates()
		case <-w.stop:
			// Final flush before shutdown
			w.flushJobUpdates()
			return
		}
	}
}

// flushJobUpdates writes all queued job updates to database
func (w *Worker) flushJobUpdates() {
	w.jobUpdateMu.Lock()
	if len(w.jobUpdateQueue) == 0 {
		w.jobUpdateMu.Unlock()
		return
	}

	// Copy queue and clear it
	jobsToUpdate := make([]*model.Job, 0, len(w.jobUpdateQueue))
	for _, job := range w.jobUpdateQueue {
		jobsToUpdate = append(jobsToUpdate, job)
	}
	w.jobUpdateQueue = make(map[string]*model.Job)
	w.jobUpdateMu.Unlock()

	// Write all updates (with retry logic in SaveJob)
	for _, job := range jobsToUpdate {
		if err := w.repo.SaveJob(job); err != nil {
			logger.WithFields(map[string]interface{}{
				"job_id": job.ID,
			}).Warn("Failed to flush job update: %v", err)
		}
	}
}

// FilterResult represents the result of applying deterministic filters
type FilterResult struct {
	ShouldFlag   bool
	FilterReason string
	Score        float64
	Reason       string
}

// applyFilters applies deterministic filters (fast, cheap checks)
func (w *Worker) applyFilters(tweet *model.TweetRecord) *FilterResult {
	if w.policy == nil {
		return &FilterResult{ShouldFlag: false}
	}

	text := strings.ToLower(tweet.Text)

	// Check for blocked phrases
	for _, phrase := range w.policy.BlockedPhrases {
		if phrase != "" && strings.Contains(text, strings.ToLower(phrase)) {
			return &FilterResult{
				ShouldFlag:   true,
				FilterReason: "blocked_phrase",
				Score:        1.0,
				Reason:       "Contains blocked phrase: " + phrase,
			}
		}
	}

	// Threat patterns
	threatPatterns := []string{
		"kill yourself", "kys", "go die", "should die",
		"i'll kill you", "going to kill", "threaten to",
	}
	for _, pattern := range threatPatterns {
		if strings.Contains(text, pattern) {
			return &FilterResult{
				ShouldFlag:   true,
				FilterReason: "threat",
				Score:        1.0,
				Reason:       "Contains threatening language",
			}
		}
	}

	// Severe hate speech
	severePatterns := []string{
		"deserves to die", "should be killed",
	}
	for _, pattern := range severePatterns {
		if strings.Contains(text, pattern) {
			return &FilterResult{
				ShouldFlag:   true,
				FilterReason: "hate_speech",
				Score:        0.95,
				Reason:       "Contains hate speech patterns",
			}
		}
	}

	return &FilterResult{ShouldFlag: false}
}

// readerAtWrapper implements io.ReaderAt for in-memory data
type readerAtWrapper struct {
	data []byte
}

func (r *readerAtWrapper) ReadAt(p []byte, off int64) (n int, err error) {
	if off >= int64(len(r.data)) {
		return 0, io.EOF
	}
	n = copy(p, r.data[off:])
	if n < len(p) {
		err = io.EOF
	}
	return n, err
}
