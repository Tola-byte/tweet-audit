package repository

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// initDB initializes the SQLite database and creates tables if they don't exist
func initDB(dbPath string) (*sql.DB, error) {
	// Ensure directory exists
	dir := filepath.Dir(dbPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create db directory: %w", err)
		}
	}

	// Enable WAL mode for better concurrency (allows multiple readers + one writer)
	// Use query parameter to enable WAL mode
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool for SQLite
	// SQLite works best with a single connection, but we allow a small pool for concurrent reads
	db.SetMaxOpenConns(1)        // SQLite only allows one writer at a time
	db.SetMaxIdleConns(1)        // Keep one idle connection
	db.SetConnMaxLifetime(0)    // Connections don't expire

	// Enable WAL mode explicitly (in case query param didn't work)
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}

	// Set busy timeout to 5 seconds (retry for up to 5s on lock)
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set busy timeout: %w", err)
	}

	// Create tables
	if err := createTables(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create tables: %w", err)
	}

	return db, nil
}

func createTables(db *sql.DB) error {
	schema := `
	-- Jobs table: persist job state for resume capability
	CREATE TABLE IF NOT EXISTS jobs (
		id TEXT PRIMARY KEY,
		file_id TEXT NOT NULL,
		status TEXT NOT NULL,
		processed_count INTEGER DEFAULT 0,
		total_count INTEGER DEFAULT 0,
		gemini_calls INTEGER DEFAULT 0,
		flagged_count INTEGER DEFAULT 0,
		criteria TEXT, -- JSON: ModerationCriteria
		error TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);

	-- Tweets table: store all parsed tweets for queue processing
	CREATE TABLE IF NOT EXISTS tweets (
		id TEXT PRIMARY KEY,
		job_id TEXT NOT NULL,
		tweet_id TEXT NOT NULL,
		text TEXT NOT NULL,
		created_at TEXT NOT NULL,
		links TEXT, -- JSON array
		attachments TEXT, -- JSON array
		hashtags TEXT, -- JSON array
		mentions TEXT, -- JSON array
		needs_gemini INTEGER DEFAULT 0, -- 0 = no, 1 = yes
		processed INTEGER DEFAULT 0, -- 0 = no, 1 = yes
		filter_result TEXT, -- JSON: {should_flag, reason, score}
		created_at_db TEXT NOT NULL
	);

	-- Flagged tweets (existing)
	CREATE TABLE IF NOT EXISTS flagged_tweets (
		id TEXT PRIMARY KEY,
		tweet_id TEXT NOT NULL,
		job_id TEXT NOT NULL,
		text TEXT NOT NULL,
		created_at TEXT NOT NULL,
		flagged_at TEXT NOT NULL,
		reason TEXT NOT NULL,
		score REAL,
		url TEXT NOT NULL,
		filter_reason TEXT
	);

	-- Quota tracking for daily API limits
	CREATE TABLE IF NOT EXISTS quota_tracking (
		date TEXT PRIMARY KEY, -- YYYY-MM-DD format
		api_calls INTEGER DEFAULT 0,
		last_reset TEXT NOT NULL
	);

	-- Indexes
	CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
	CREATE INDEX IF NOT EXISTS idx_tweets_job_id ON tweets(job_id);
	CREATE INDEX IF NOT EXISTS idx_tweets_needs_gemini ON tweets(job_id, needs_gemini, processed);
	CREATE INDEX IF NOT EXISTS idx_flagged_tweets_job_id ON flagged_tweets(job_id);
	CREATE INDEX IF NOT EXISTS idx_flagged_tweets_tweet_id ON flagged_tweets(tweet_id);
	`

	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	return nil
}
