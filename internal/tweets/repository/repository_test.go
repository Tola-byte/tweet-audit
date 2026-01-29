package repository

import (
	"os"
	"strconv"
	"testing"
	"time"

	"tweet-audit/internal/tweets/model"
)

func setupTestDB(t *testing.T) (*Repository, func()) {
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	tmpfile.Close()

	repo, err := NewRepository(tmpfile.Name(), 5*time.Second)
	if err != nil {
		os.Remove(tmpfile.Name())
		t.Fatalf("Failed to create repository: %v", err)
	}

	cleanup := func() {
		repo.Close()
		os.Remove(tmpfile.Name())
	}

	return repo, cleanup
}

func TestSaveJob(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	job := &model.Job{
		ID:             "test-job-1",
		FileID:         "test-file-1",
		Status:         model.JobQueued,
		ProcessedCount: 0,
		TotalCount:     100,
		GeminiCalls:    0,
		FlaggedCount:   0,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	err := repo.SaveJob(job)
	if err != nil {
		t.Fatalf("SaveJob failed: %v", err)
	}

	// Retrieve and verify
	retrieved, err := repo.GetJob("test-job-1")
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if retrieved == nil {
		t.Fatal("GetJob returned nil")
	}

	if retrieved.ID != job.ID {
		t.Errorf("Expected ID %s, got %s", job.ID, retrieved.ID)
	}
	if retrieved.FileID != job.FileID {
		t.Errorf("Expected FileID %s, got %s", job.FileID, retrieved.FileID)
	}
	if retrieved.Status != job.Status {
		t.Errorf("Expected Status %s, got %s", job.Status, retrieved.Status)
	}
	if retrieved.TotalCount != job.TotalCount {
		t.Errorf("Expected TotalCount %d, got %d", job.TotalCount, retrieved.TotalCount)
	}
}

func TestSaveJob_WithCriteria(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	criteria := &model.ModerationCriteria{
		ForbiddenWords:    []string{"crypto", "NFT"},
		ProfessionalCheck: true,
		ExcludePolitics:   true,
	}

	job := &model.Job{
		ID:             "test-job-2",
		FileID:         "test-file-2",
		Status:         model.JobQueued,
		ProcessedCount: 0,
		TotalCount:     50,
		Criteria:       criteria,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	err := repo.SaveJob(job)
	if err != nil {
		t.Fatalf("SaveJob failed: %v", err)
	}

	retrieved, err := repo.GetJob("test-job-2")
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if retrieved.Criteria == nil {
		t.Fatal("Criteria was not saved")
	}
	if len(retrieved.Criteria.ForbiddenWords) != 2 {
		t.Errorf("Expected 2 forbidden words, got %d", len(retrieved.Criteria.ForbiddenWords))
	}
	if !retrieved.Criteria.ProfessionalCheck {
		t.Error("ProfessionalCheck should be true")
	}
}

func TestSaveJob_Update(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	job := &model.Job{
		ID:             "test-job-3",
		FileID:         "test-file-3",
		Status:         model.JobQueued,
		ProcessedCount: 0,
		TotalCount:     100,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	err := repo.SaveJob(job)
	if err != nil {
		t.Fatalf("SaveJob failed: %v", err)
	}

	// Update the job
	job.Status = model.JobRunning
	job.ProcessedCount = 50
	err = repo.SaveJob(job)
	if err != nil {
		t.Fatalf("SaveJob update failed: %v", err)
	}

	retrieved, err := repo.GetJob("test-job-3")
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if retrieved.Status != model.JobRunning {
		t.Errorf("Expected Status %s, got %s", model.JobRunning, retrieved.Status)
	}
	if retrieved.ProcessedCount != 50 {
		t.Errorf("Expected ProcessedCount 50, got %d", retrieved.ProcessedCount)
	}
}

func TestGetJob_NotFound(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	job, err := repo.GetJob("non-existent")
	if err != nil {
		t.Fatalf("GetJob should not return error for non-existent job: %v", err)
	}
	if job != nil {
		t.Error("GetJob should return nil for non-existent job")
	}
}

func TestSaveTweet(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	tweet := &model.TweetRecord{
		ID:          "tweet-123",
		Text:        "Test tweet",
		CreatedAt:   time.Now(),
		Links:       []string{"https://example.com"},
		Hashtags:    []string{"#test"},
		Mentions:    []string{"@user"},
		Attachments: []string{"https://example.com/img.jpg"},
	}

	err := repo.SaveTweet("job-1", tweet)
	if err != nil {
		t.Fatalf("SaveTweet failed: %v", err)
	}
}

func TestMarkTweetNeedsGemini(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// First save a tweet
	tweet := &model.TweetRecord{
		ID:        "tweet-456",
		Text:      "Test tweet",
		CreatedAt: time.Now(),
	}
	err := repo.SaveTweet("job-1", tweet)
	if err != nil {
		t.Fatalf("SaveTweet failed: %v", err)
	}

	// Mark it as needing Gemini
	err = repo.MarkTweetNeedsGemini("job-1", "tweet-456")
	if err != nil {
		t.Fatalf("MarkTweetNeedsGemini failed: %v", err)
	}

	// Verify it's marked
	tweets, err := repo.GetUnprocessedTweetsNeedingGemini("job-1", 10)
	if err != nil {
		t.Fatalf("GetUnprocessedTweetsNeedingGemini failed: %v", err)
	}
	if len(tweets) != 1 {
		t.Fatalf("Expected 1 tweet needing Gemini, got %d", len(tweets))
	}
	if tweets[0].ID != "tweet-456" {
		t.Errorf("Expected tweet ID 'tweet-456', got '%s'", tweets[0].ID)
	}
}

func TestMarkTweetProcessed(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	tweet := &model.TweetRecord{
		ID:        "tweet-789",
		Text:      "Test tweet",
		CreatedAt: time.Now(),
	}
	err := repo.SaveTweet("job-1", tweet)
	if err != nil {
		t.Fatalf("SaveTweet failed: %v", err)
	}

	err = repo.MarkTweetNeedsGemini("job-1", "tweet-789")
	if err != nil {
		t.Fatalf("MarkTweetNeedsGemini failed: %v", err)
	}

	err = repo.MarkTweetProcessed("job-1", "tweet-789")
	if err != nil {
		t.Fatalf("MarkTweetProcessed failed: %v", err)
	}

	// Should not appear in unprocessed list
	tweets, err := repo.GetUnprocessedTweetsNeedingGemini("job-1", 10)
	if err != nil {
		t.Fatalf("GetUnprocessedTweetsNeedingGemini failed: %v", err)
	}
	if len(tweets) != 0 {
		t.Errorf("Expected 0 unprocessed tweets, got %d", len(tweets))
	}
}

func TestSaveFlaggedTweet(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	flagged := &model.FlaggedTweet{
		ID:        "flagged-1",
		TweetID:   "tweet-123",
		JobID:     "job-1",
		Text:      "Abusive tweet",
		CreatedAt: time.Now(),
		FlaggedAt: time.Now(),
		Reason:    "Contains abusive language",
		Score:     0.85,
		URL:       "https://twitter.com/user/status/123",
	}

	err := repo.SaveFlaggedTweet(flagged)
	if err != nil {
		t.Fatalf("SaveFlaggedTweet failed: %v", err)
	}

	// Retrieve and verify
	tweets, err := repo.GetFlaggedTweets("job-1")
	if err != nil {
		t.Fatalf("GetFlaggedTweets failed: %v", err)
	}
	if len(tweets) != 1 {
		t.Fatalf("Expected 1 flagged tweet, got %d", len(tweets))
	}
	if tweets[0].ID != "flagged-1" {
		t.Errorf("Expected ID 'flagged-1', got '%s'", tweets[0].ID)
	}
	if tweets[0].Score != 0.85 {
		t.Errorf("Expected score 0.85, got %f", tweets[0].Score)
	}
}

func TestGetFlaggedTweets_Empty(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	tweets, err := repo.GetFlaggedTweets("job-1")
	if err != nil {
		t.Fatalf("GetFlaggedTweets failed: %v", err)
	}
	if len(tweets) != 0 {
		t.Errorf("Expected 0 flagged tweets, got %d", len(tweets))
	}
}

func TestListFlaggedTweets_WithJobID(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Create flagged tweets for two different jobs
	for i := 0; i < 5; i++ {
		idStr := strconv.Itoa(i)
		flagged := &model.FlaggedTweet{
			ID:        "flagged-job1-" + idStr,
			TweetID:   "tweet-" + idStr,
			JobID:     "job-1",
			Text:      "Tweet " + idStr,
			CreatedAt: time.Now(),
			FlaggedAt: time.Now(),
			Reason:    "Test",
			Score:     0.5,
		}
		repo.SaveFlaggedTweet(flagged)
	}

	for i := 0; i < 3; i++ {
		idStr := strconv.Itoa(i)
		flagged := &model.FlaggedTweet{
			ID:        "flagged-job2-" + idStr,
			TweetID:   "tweet-" + idStr,
			JobID:     "job-2",
			Text:      "Tweet " + idStr,
			CreatedAt: time.Now(),
			FlaggedAt: time.Now(),
			Reason:    "Test",
			Score:     0.5,
		}
		repo.SaveFlaggedTweet(flagged)
	}

	// List only job-1 tweets
	tweets, err := repo.ListFlaggedTweets("job-1", 10, 0)
	if err != nil {
		t.Fatalf("ListFlaggedTweets failed: %v", err)
	}
	if len(tweets) != 5 {
		t.Errorf("Expected 5 tweets for job-1, got %d", len(tweets))
	}

	// List all tweets
	allTweets, err := repo.ListFlaggedTweets("", 10, 0)
	if err != nil {
		t.Fatalf("ListFlaggedTweets failed: %v", err)
	}
	if len(allTweets) != 8 {
		t.Errorf("Expected 8 total tweets, got %d", len(allTweets))
	}
}

func TestListFlaggedTweets_Pagination(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Create 10 flagged tweets
	for i := 0; i < 10; i++ {
		idStr := strconv.Itoa(i)
		flagged := &model.FlaggedTweet{
			ID:        "flagged-" + idStr,
			TweetID:   "tweet-" + idStr,
			JobID:     "job-1",
			Text:      "Tweet " + idStr,
			CreatedAt: time.Now(),
			FlaggedAt: time.Now(),
			Reason:    "Test",
			Score:     0.5,
		}
		repo.SaveFlaggedTweet(flagged)
	}

	// First page
	page1, err := repo.ListFlaggedTweets("job-1", 5, 0)
	if err != nil {
		t.Fatalf("ListFlaggedTweets failed: %v", err)
	}
	if len(page1) != 5 {
		t.Errorf("Expected 5 tweets on page 1, got %d", len(page1))
	}

	// Second page
	page2, err := repo.ListFlaggedTweets("job-1", 5, 5)
	if err != nil {
		t.Fatalf("ListFlaggedTweets failed: %v", err)
	}
	if len(page2) != 5 {
		t.Errorf("Expected 5 tweets on page 2, got %d", len(page2))
	}

	// Verify no overlap
	ids1 := make(map[string]bool)
	for _, tweet := range page1 {
		ids1[tweet.ID] = true
	}
	for _, tweet := range page2 {
		if ids1[tweet.ID] {
			t.Errorf("Found duplicate tweet ID: %s", tweet.ID)
		}
	}
}

func TestCountFlaggedTweets(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Create tweets for two jobs
	for i := 0; i < 3; i++ {
		idStr := strconv.Itoa(i)
		flagged := &model.FlaggedTweet{
			ID:        "flagged-job1-" + idStr,
			TweetID:   "tweet-" + idStr,
			JobID:     "job-1",
			Text:      "Tweet",
			CreatedAt: time.Now(),
			FlaggedAt: time.Now(),
			Reason:    "Test",
			Score:     0.5,
		}
		repo.SaveFlaggedTweet(flagged)
	}

	for i := 0; i < 2; i++ {
		idStr := strconv.Itoa(i)
		flagged := &model.FlaggedTweet{
			ID:        "flagged-job2-" + idStr,
			TweetID:   "tweet-" + idStr,
			JobID:     "job-2",
			Text:      "Tweet",
			CreatedAt: time.Now(),
			FlaggedAt: time.Now(),
			Reason:    "Test",
			Score:     0.5,
		}
		repo.SaveFlaggedTweet(flagged)
	}

	count1, err := repo.CountFlaggedTweets("job-1")
	if err != nil {
		t.Fatalf("CountFlaggedTweets failed: %v", err)
	}
	if count1 != 3 {
		t.Errorf("Expected 3 tweets for job-1, got %d", count1)
	}

	total, err := repo.CountFlaggedTweets("")
	if err != nil {
		t.Fatalf("CountFlaggedTweets failed: %v", err)
	}
	if total != 5 {
		t.Errorf("Expected 5 total tweets, got %d", total)
	}
}

func TestGetFlaggedTweetByID(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	flagged := &model.FlaggedTweet{
		ID:        "flagged-123",
		TweetID:   "tweet-123",
		JobID:     "job-1",
		Text:      "Test tweet",
		CreatedAt: time.Now(),
		FlaggedAt: time.Now(),
		Reason:    "Test reason",
		Score:     0.75,
		URL:       "https://twitter.com/user/status/123",
	}

	err := repo.SaveFlaggedTweet(flagged)
	if err != nil {
		t.Fatalf("SaveFlaggedTweet failed: %v", err)
	}

	retrieved, err := repo.GetFlaggedTweetByID("flagged-123")
	if err != nil {
		t.Fatalf("GetFlaggedTweetByID failed: %v", err)
	}
	if retrieved == nil {
		t.Fatal("GetFlaggedTweetByID returned nil")
	}
	if retrieved.ID != "flagged-123" {
		t.Errorf("Expected ID 'flagged-123', got '%s'", retrieved.ID)
	}
	if retrieved.Score != 0.75 {
		t.Errorf("Expected score 0.75, got %f", retrieved.Score)
	}

	// Test non-existent
	notFound, err := repo.GetFlaggedTweetByID("non-existent")
	if err != nil {
		t.Fatalf("GetFlaggedTweetByID should not return error: %v", err)
	}
	if notFound != nil {
		t.Error("GetFlaggedTweetByID should return nil for non-existent ID")
	}
}

func TestQuotaTracking(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Initial quota should be 0
	quota, err := repo.GetTodayQuotaUsage()
	if err != nil {
		t.Fatalf("GetTodayQuotaUsage failed: %v", err)
	}
	if quota != 0 {
		t.Errorf("Expected initial quota 0, got %d", quota)
	}

	// Increment quota
	err = repo.IncrementQuotaUsage()
	if err != nil {
		t.Fatalf("IncrementQuotaUsage failed: %v", err)
	}

	quota, err = repo.GetTodayQuotaUsage()
	if err != nil {
		t.Fatalf("GetTodayQuotaUsage failed: %v", err)
	}
	if quota != 1 {
		t.Errorf("Expected quota 1, got %d", quota)
	}

	// Increment multiple times
	for i := 0; i < 5; i++ {
		repo.IncrementQuotaUsage()
	}

	quota, err = repo.GetTodayQuotaUsage()
	if err != nil {
		t.Fatalf("GetTodayQuotaUsage failed: %v", err)
	}
	if quota != 6 {
		t.Errorf("Expected quota 6, got %d", quota)
	}
}
