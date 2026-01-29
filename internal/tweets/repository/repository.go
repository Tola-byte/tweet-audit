package repository

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tweet-audit/internal/tweets/model"
)

// Repository handles persistence for the tweets domain.
type Repository struct {
	db *sql.DB
}

// NewRepository creates a new repository with SQLite database
// dbPath is the path to the SQLite database file (e.g., "data/tweet-audit.db")
// busyTimeout is the SQLite busy timeout duration
func NewRepository(dbPath string, busyTimeout time.Duration) (*Repository, error) {
	db, err := initDB(dbPath, busyTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	return &Repository{db: db}, nil
}

// Job persistence methods

// SaveJob saves or updates a job
func (r *Repository) SaveJob(job *model.Job) error {
	var criteriaJSON string
	if job.Criteria != nil {
		criteriaBytes, err := json.Marshal(job.Criteria)
		if err != nil {
			return fmt.Errorf("failed to marshal criteria: %w", err)
		}
		criteriaJSON = string(criteriaBytes)
	}

	query := `
		INSERT INTO jobs (id, file_id, status, processed_count, total_count, gemini_calls, flagged_count, criteria, error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			status = excluded.status,
			processed_count = excluded.processed_count,
			total_count = excluded.total_count,
			gemini_calls = excluded.gemini_calls,
			flagged_count = excluded.flagged_count,
			criteria = excluded.criteria,
			error = excluded.error,
			updated_at = excluded.updated_at
	`
	_, err := r.db.Exec(query,
		job.ID,
		job.FileID,
		string(job.Status),
		job.ProcessedCount,
		job.TotalCount,
		job.GeminiCalls,
		job.FlaggedCount,
		criteriaJSON,
		job.Error,
		job.CreatedAt.Format(time.RFC3339),
		job.UpdatedAt.Format(time.RFC3339),
	)
	return err
}

// GetJob retrieves a job by ID
func (r *Repository) GetJob(id string) (*model.Job, error) {
	query := `
		SELECT id, file_id, status, processed_count, total_count, gemini_calls, flagged_count, criteria, error, created_at, updated_at
		FROM jobs
		WHERE id = ?
	`
	var job model.Job
	var statusStr, createdAtStr, updatedAtStr, criteriaJSON sql.NullString
	err := r.db.QueryRow(query, id).Scan(
		&job.ID,
		&job.FileID,
		&statusStr,
		&job.ProcessedCount,
		&job.TotalCount,
		&job.GeminiCalls,
		&job.FlaggedCount,
		&criteriaJSON,
		&job.Error,
		&createdAtStr,
		&updatedAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	job.Status = model.JobStatus(statusStr.String)
	job.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr.String)
	job.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAtStr.String)

	if criteriaJSON.Valid && criteriaJSON.String != "" {
		var criteria model.ModerationCriteria
		if err := json.Unmarshal([]byte(criteriaJSON.String), &criteria); err == nil {
			job.Criteria = &criteria
		}
	}

	return &job, nil
}

// Tweet persistence methods

// SaveTweet saves a tweet to the database
func (r *Repository) SaveTweet(jobID string, tweet *model.TweetRecord) error {
	linksJSON, _ := json.Marshal(tweet.Links)
	attachmentsJSON, _ := json.Marshal(tweet.Attachments)
	hashtagsJSON, _ := json.Marshal(tweet.Hashtags)
	mentionsJSON, _ := json.Marshal(tweet.Mentions)

	query := `
		INSERT INTO tweets (id, job_id, tweet_id, text, created_at, links, attachments, hashtags, mentions, needs_gemini, processed, created_at_db)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?)
	`
	id := fmt.Sprintf("%s_%s", jobID, tweet.ID)
	return r.execWithRetry(query,
		id,
		jobID,
		tweet.ID,
		tweet.Text,
		tweet.CreatedAt.Format(time.RFC3339),
		string(linksJSON),
		string(attachmentsJSON),
		string(hashtagsJSON),
		string(mentionsJSON),
		time.Now().Format(time.RFC3339),
	)
}

// GetUnprocessedTweetsNeedingGemini returns tweets that need Gemini scoring
func (r *Repository) GetUnprocessedTweetsNeedingGemini(jobID string, limit int) ([]*model.TweetRecord, error) {
	query := `
		SELECT id, tweet_id, text, created_at, links, attachments, hashtags, mentions
		FROM tweets
		WHERE job_id = ? AND needs_gemini = 1 AND processed = 0
		ORDER BY created_at_db
		LIMIT ?
	`
	rows, err := r.db.Query(query, jobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tweets []*model.TweetRecord
	for rows.Next() {
		var t model.TweetRecord
		var createdAtStr, linksJSON, attachmentsJSON, hashtagsJSON, mentionsJSON string
		var dbID string

		err := rows.Scan(
			&dbID,
			&t.ID,
			&t.Text,
			&createdAtStr,
			&linksJSON,
			&attachmentsJSON,
			&hashtagsJSON,
			&mentionsJSON,
		)
		if err != nil {
			return nil, err
		}

		t.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
		json.Unmarshal([]byte(linksJSON), &t.Links)
		json.Unmarshal([]byte(attachmentsJSON), &t.Attachments)
		json.Unmarshal([]byte(hashtagsJSON), &t.Hashtags)
		json.Unmarshal([]byte(mentionsJSON), &t.Mentions)

		tweets = append(tweets, &t)
	}
	return tweets, rows.Err()
}

// MarkTweetNeedsGemini marks a tweet as needing Gemini scoring
func (r *Repository) MarkTweetNeedsGemini(jobID, tweetID string) error {
	query := `UPDATE tweets SET needs_gemini = 1 WHERE job_id = ? AND tweet_id = ?`
	return r.execWithRetry(query, jobID, tweetID)
}

// MarkTweetProcessed marks a tweet as processed
func (r *Repository) MarkTweetProcessed(jobID, tweetID string) error {
	query := `UPDATE tweets SET processed = 1 WHERE job_id = ? AND tweet_id = ?`
	return r.execWithRetry(query, jobID, tweetID)
}

// Quota tracking methods

// GetTodayQuotaUsage returns how many API calls have been made today
func (r *Repository) GetTodayQuotaUsage() (int, error) {
	today := time.Now().Format("2006-01-02")
	query := `SELECT api_calls FROM quota_tracking WHERE date = ?`
	var calls int
	err := r.db.QueryRow(query, today).Scan(&calls)
	if err == sql.ErrNoRows {
		// First call today, initialize
		return 0, nil
	}
	return calls, err
}

// IncrementQuotaUsage increments the daily API call count
func (r *Repository) IncrementQuotaUsage() error {
	today := time.Now().Format("2006-01-02")
	query := `
		INSERT INTO quota_tracking (date, api_calls, last_reset)
		VALUES (?, 1, ?)
		ON CONFLICT(date) DO UPDATE SET
			api_calls = api_calls + 1,
			last_reset = excluded.last_reset
	`
	return r.execWithRetry(query, today, time.Now().Format(time.RFC3339))
}

// SaveFlaggedTweet saves a flagged tweet
func (r *Repository) SaveFlaggedTweet(ft *model.FlaggedTweet) error {
	query := `
		INSERT INTO flagged_tweets (id, tweet_id, job_id, text, created_at, flagged_at, reason, score, url, filter_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	return r.execWithRetry(query,
		ft.ID,
		ft.TweetID,
		ft.JobID,
		ft.Text,
		ft.CreatedAt.Format(time.RFC3339),
		ft.FlaggedAt.Format(time.RFC3339),
		ft.Reason,
		ft.Score,
		ft.URL,
		ft.FilterReason,
	)
}

// GetFlaggedTweets retrieves flagged tweets for a job
func (r *Repository) GetFlaggedTweets(jobID string) ([]*model.FlaggedTweet, error) {
	query := `
		SELECT id, tweet_id, job_id, text, created_at, flagged_at, reason, score, url, filter_reason
		FROM flagged_tweets
		WHERE job_id = ?
		ORDER BY flagged_at DESC
	`

	rows, err := r.db.Query(query, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tweets []*model.FlaggedTweet
	for rows.Next() {
		var ft model.FlaggedTweet
		var createdAtStr, flaggedAtStr string

		err := rows.Scan(
			&ft.ID,
			&ft.TweetID,
			&ft.JobID,
			&ft.Text,
			&createdAtStr,
			&flaggedAtStr,
			&ft.Reason,
			&ft.Score,
			&ft.URL,
			&ft.FilterReason,
		)
		if err != nil {
			return nil, err
		}

		ft.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
		ft.FlaggedAt, _ = time.Parse(time.RFC3339, flaggedAtStr)
		tweets = append(tweets, &ft)
	}

	return tweets, rows.Err()
}

// ListFlaggedTweets retrieves flagged tweets with pagination and optional job filter
func (r *Repository) ListFlaggedTweets(jobID string, limit, offset int) ([]*model.FlaggedTweet, error) {
	var query string
	var args []interface{}

	if jobID != "" {
		query = `
			SELECT id, tweet_id, job_id, text, created_at, flagged_at, reason, score, url, filter_reason
			FROM flagged_tweets
			WHERE job_id = ?
			ORDER BY flagged_at DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{jobID, limit, offset}
	} else {
		query = `
			SELECT id, tweet_id, job_id, text, created_at, flagged_at, reason, score, url, filter_reason
			FROM flagged_tweets
			ORDER BY flagged_at DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{limit, offset}
	}

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tweets []*model.FlaggedTweet
	for rows.Next() {
		var ft model.FlaggedTweet
		var createdAtStr, flaggedAtStr string

		err := rows.Scan(
			&ft.ID,
			&ft.TweetID,
			&ft.JobID,
			&ft.Text,
			&createdAtStr,
			&flaggedAtStr,
			&ft.Reason,
			&ft.Score,
			&ft.URL,
			&ft.FilterReason,
		)
		if err != nil {
			return nil, err
		}

		ft.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
		ft.FlaggedAt, _ = time.Parse(time.RFC3339, flaggedAtStr)
		tweets = append(tweets, &ft)
	}

	return tweets, rows.Err()
}

// GetFlaggedTweetByID retrieves a specific flagged tweet by its internal ID
func (r *Repository) GetFlaggedTweetByID(id string) (*model.FlaggedTweet, error) {
	query := `
		SELECT id, tweet_id, job_id, text, created_at, flagged_at, reason, score, url, filter_reason
		FROM flagged_tweets
		WHERE id = ?
	`

	var ft model.FlaggedTweet
	var createdAtStr, flaggedAtStr string

	err := r.db.QueryRow(query, id).Scan(
		&ft.ID,
		&ft.TweetID,
		&ft.JobID,
		&ft.Text,
		&createdAtStr,
		&flaggedAtStr,
		&ft.Reason,
		&ft.Score,
		&ft.URL,
		&ft.FilterReason,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	ft.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
	ft.FlaggedAt, _ = time.Parse(time.RFC3339, flaggedAtStr)

	return &ft, nil
}

// CountFlaggedTweets returns the total count of flagged tweets, optionally filtered by job
func (r *Repository) CountFlaggedTweets(jobID string) (int, error) {
	var query string
	var args []interface{}

	if jobID != "" {
		query = `SELECT COUNT(*) FROM flagged_tweets WHERE job_id = ?`
		args = []interface{}{jobID}
	} else {
		query = `SELECT COUNT(*) FROM flagged_tweets`
		args = []interface{}{}
	}

	var count int
	err := r.db.QueryRow(query, args...).Scan(&count)
	return count, err
}

// execWithRetry executes a query with retry logic for SQLITE_BUSY errors
// SQLite can get locked when multiple goroutines write concurrently
// This retries up to 5 times with exponential backoff
func (r *Repository) execWithRetry(query string, args ...interface{}) error {
	maxRetries := 5
	backoff := 10 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		_, err := r.db.Exec(query, args...)
		if err == nil {
			return nil
		}

		// Check if it's a SQLITE_BUSY error
		errStr := err.Error()
		if strings.Contains(errStr, "database is locked") ||
			strings.Contains(errStr, "SQLITE_BUSY") ||
			strings.Contains(errStr, "locked") {
			// Retry with exponential backoff
			if attempt < maxRetries-1 {
				time.Sleep(backoff)
				backoff *= 2 // Exponential backoff: 10ms, 20ms, 40ms, 80ms, 160ms
				continue
			}
		}

		// Not a retryable error or max retries reached
		return err
	}

	return errors.New("max retries exceeded for database operation")
}

// Close closes the database connection
func (r *Repository) Close() error {
	if r.db != nil {
		return r.db.Close()
	}
	return nil
}
