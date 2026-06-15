package model

import (
	"context"
	"io"
	"time"
)

type StoredFile struct {
	ID           string    `json:"id"`
	OriginalName string    `json:"original_name"`
	ContentType  string    `json:"content_type"`
	Size         int64     `json:"size"`
	URL          string    `json:"url,omitempty"`
	Storage      string    `json:"storage_backend,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type FileStore interface {
	Save(ctx context.Context, name string, r io.Reader) (*StoredFile, error)
	Open(ctx context.Context, id string) (io.ReadCloser, error)
	Delete(ctx context.Context, id string) error
}

type JobStatus string

const (
	JobQueued       JobStatus = "queued"
	JobRunning      JobStatus = "running"
	JobDone         JobStatus = "done"
	JobFailed       JobStatus = "failed"
	JobPausedQuota  JobStatus = "paused_quota"  
	JobPausedManual JobStatus = "paused_manual" 
)

// JobResult represents the result of a completed job
type JobResult struct {
	FlaggedCount int `json:"flagged_count" example:"45"` // Number of tweets flagged
}

type Job struct {
	ID             string              `json:"id" example:"abc123"`                                                                    // Unique job identifier
	FileID         string              `json:"file_id" example:"file-xyz"`                                                             // ID of the uploaded archive file
	Status         JobStatus           `json:"status" example:"running" enums:"queued,running,done,failed,paused_quota,paused_manual"` // Current job status
	ProcessedCount int                 `json:"processed_count" example:"1500"`                                                         // Number of tweets processed so far
	TotalCount     int                 `json:"total_count" example:"2000"`                                                             // Total number of tweets to process
	GeminiCalls    int                 `json:"gemini_calls" example:"1200"`                                                            // Number of Gemini API calls made for this job
	FlaggedCount   int                 `json:"flagged_count" example:"45"`                                                             // Number of tweets flagged for deletion
	Criteria       *ModerationCriteria `json:"criteria,omitempty"`                                                                     // Moderation criteria used for this job (if custom criteria provided)
	Result         *JobResult          `json:"result,omitempty"`                                                                       // Final job result (only present when status is "done")
	Error          string              `json:"error,omitempty" example:"Failed to parse archive"`                                      // Error message (only present if status is "failed")
	CreatedAt      time.Time           `json:"created_at" example:"2024-01-18T10:30:00Z"`                                              // When the job was created
	UpdatedAt      time.Time           `json:"updated_at" example:"2024-01-18T10:45:00Z"`                                              // When the job was last updated
}

type UploadResponse struct {
	File  *StoredFile `json:"file"`
	JobID string      `json:"job_id"`
}

// TweetRecord represents a normalized tweet from an X/Twitter archive
type TweetRecord struct {
	ID          string    `json:"id"`          // Tweet ID (from archive)
	Text        string    `json:"text"`        // Full tweet text
	CreatedAt   time.Time `json:"created_at"`  // When the tweet was posted
	Links       []string  `json:"links"`       // URLs found in the tweet
	Attachments []string  `json:"attachments"` // Media attachment URLs
	Hashtags    []string  `json:"hashtags"`    // Hashtags in the tweet
	Mentions    []string  `json:"mentions"`    // @mentions in the tweet
}


type FlaggedTweet struct {
	ID           string    `json:"id" example:"internal-uuid-123"`                                       // Internal unique identifier
	TweetID      string    `json:"tweet_id" example:"1649447064454017026"`                               // Original Twitter/X tweet ID
	JobID        string    `json:"job_id" example:"job-abc123"`                                          // ID of the job that processed this tweet
	Text         string    `json:"text" example:"This is a problematic tweet"`                           // Full text of the tweet
	CreatedAt    time.Time `json:"created_at" example:"2023-04-15T10:30:00Z"`                            // When the tweet was originally posted
	FlaggedAt    time.Time `json:"flagged_at" example:"2024-01-18T12:45:30Z"`                            // When the tweet was flagged by the system
	Reason       string    `json:"reason" example:"Contains severe profanity used aggressively"`         // Explanation of why the tweet was flagged
	Score        float64   `json:"score" example:"0.85"`                                                 // Confidence score (0.0-1.0, higher = more likely to flag)
	URL          string    `json:"url" example:"https://twitter.com/i/web/status/1649447064454017026"`   // Tweet URL for manual deletion
	FilterReason string    `json:"filter_reason" example:"llm_scorer" enums:"llm_scorer,blocked_phrase"` // Which filter caught it: "llm_scorer" (Gemini) or "blocked_phrase" (deterministic)
}

type ScoreResult struct {
	ShouldFlag bool     `json:"should_flag"` // Whether the tweet should be flagged
	Score      float64  `json:"score"`       // Confidence score (0-1, higher = more likely to flag)
	Reason     string   `json:"reason"`      // Explanation for the score
	Labels     []string `json:"labels"`      // Optional labels (e.g., ["unprofessional", "outdated"])
}


type ModerationCriteria struct {
	ForbiddenWords    []string `json:"forbidden_words" example:"crypto,NFT,hustlegrindset"` // Words or phrases that should be flagged (case-insensitive)
	ProfessionalCheck bool     `json:"professional_check" example:"true"`                   // If true, flags unprofessional language or tone
	Tone              string   `json:"tone" example:"respectful and thoughtful"`            // Expected tone. Tweets deviating significantly from this tone will be flagged
	ExcludePolitics   bool     `json:"exclude_politics" example:"true"`                     // If true, flags political content or statements
	ExcludeProfanity  bool     `json:"exclude_profanity" example:"true"`                    // If true, flags severe profanity used aggressively (default: true)
	ExcludeThreats    bool     `json:"exclude_threats" example:"true"`                      // If true, flags threats of violence or harm (default: true)
	ExcludeHateSpeech bool     `json:"exclude_hate_speech" example:"true"`                  // If true, flags hate speech or discrimination targeting groups (default: true)
	ExcludeHarassment bool     `json:"exclude_harassment" example:"true"`                   // If true, flags harassment or targeted abuse (default: true)
}

// DefaultCriteria returns sensible default moderation criteria
func DefaultCriteria() *ModerationCriteria {
	return &ModerationCriteria{
		ForbiddenWords:    []string{},
		ProfessionalCheck: false,
		Tone:              "respectful and thoughtful",
		ExcludePolitics:   false,
		ExcludeProfanity:  true,
		ExcludeThreats:    true,
		ExcludeHateSpeech: true,
		ExcludeHarassment: true,
	}
}

// Scorer evaluates tweets and returns scores indicating if they should be flagged

type Scorer interface {
	
	Score(ctx context.Context, tweet *TweetRecord) (*ScoreResult, error)

	ScoreBatch(ctx context.Context, tweets []*TweetRecord) ([]*ScoreResult, error)

	SetCriteria(criteria *ModerationCriteria)
}

// ListTweetsResponse represents the response from the List endpoint
type ListTweetsResponse struct {
	Tweets     []*FlaggedTweet `json:"tweets"`
	Pagination struct {
		Page       int `json:"page"`
		PageSize   int `json:"page_size"`
		Total      int `json:"total"`
		TotalPages int `json:"total_pages"`
	} `json:"pagination"`
}

// ExportTweetsResponse represents the JSON response from the Export endpoint
type ExportTweetsResponse struct {
	URLs  []string `json:"urls"`
	Count int      `json:"count"`
	JobID string   `json:"job_id"`
}
