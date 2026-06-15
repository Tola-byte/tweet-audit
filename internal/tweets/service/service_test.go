package service

import (
	"os"
	"strconv"
	"testing"
	"time"

	"tweet-audit/internal/tweets/model"
	"tweet-audit/internal/tweets/repository"
)

func setupTestService(t *testing.T) (*Service, func()) {
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	tmpfile.Close()

	repo, err := repository.NewRepository(tmpfile.Name(), 5*time.Second)
	if err != nil {
		os.Remove(tmpfile.Name())
		t.Fatalf("Failed to create repository: %v", err)
	}

	service := NewService(repo)

	cleanup := func() {
		repo.Close()
		os.Remove(tmpfile.Name())
	}

	return service, cleanup
}

func TestListFlaggedTweets(t *testing.T) {
	service, cleanup := setupTestService(t)
	defer cleanup()

	// Create some flagged tweets
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
			URL:       "https://twitter.com/user/status/" + idStr,
		}
		service.repo.SaveFlaggedTweet(flagged)
	}

	// Test pagination
	tweets, total, err := service.ListFlaggedTweets("job-1", 1, 5)
	if err != nil {
		t.Fatalf("ListFlaggedTweets failed: %v", err)
	}

	if len(tweets) != 5 {
		t.Errorf("Expected 5 tweets, got %d", len(tweets))
	}
	if total != 10 {
		t.Errorf("Expected total 10, got %d", total)
	}

	// Test second page
	tweets2, total2, err := service.ListFlaggedTweets("job-1", 2, 5)
	if err != nil {
		t.Fatalf("ListFlaggedTweets failed: %v", err)
	}

	if len(tweets2) != 5 {
		t.Errorf("Expected 5 tweets on page 2, got %d", len(tweets2))
	}
	if total2 != 10 {
		t.Errorf("Expected total 10, got %d", total2)
	}
}

func TestListFlaggedTweets_PaginationBounds(t *testing.T) {
	service, cleanup := setupTestService(t)
	defer cleanup()

	// Test with invalid page
	tweets, _, err := service.ListFlaggedTweets("", 0, 20)
	if err != nil {
		t.Fatalf("ListFlaggedTweets failed: %v", err)
	}
	if len(tweets) != 0 {
		t.Errorf("Expected 0 tweets for page 0, got %d", len(tweets))
	}

	// Test with invalid page size
	tweets2, _, err := service.ListFlaggedTweets("", 1, 0)
	if err != nil {
		t.Fatalf("ListFlaggedTweets failed: %v", err)
	}
	if len(tweets2) != 0 {
		t.Errorf("Expected 0 tweets for page size 0, got %d", len(tweets2))
	}

	// Test with page size > 100
	tweets3, _, err := service.ListFlaggedTweets("", 1, 200)
	if err != nil {
		t.Fatalf("ListFlaggedTweets failed: %v", err)
	}
	// Should be capped at 100
	if len(tweets3) > 100 {
		t.Errorf("Expected max 100 tweets, got %d", len(tweets3))
	}
}

func TestGetFlaggedTweet(t *testing.T) {
	service, cleanup := setupTestService(t)
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

	err := service.repo.SaveFlaggedTweet(flagged)
	if err != nil {
		t.Fatalf("SaveFlaggedTweet failed: %v", err)
	}

	retrieved, err := service.GetFlaggedTweet("flagged-123")
	if err != nil {
		t.Fatalf("GetFlaggedTweet failed: %v", err)
	}
	if retrieved == nil {
		t.Fatal("GetFlaggedTweet returned nil")
	}
	if retrieved.ID != "flagged-123" {
		t.Errorf("Expected ID 'flagged-123', got '%s'", retrieved.ID)
	}
	if retrieved.Score != 0.75 {
		t.Errorf("Expected score 0.75, got %f", retrieved.Score)
	}

	// Test non-existent
	notFound, err := service.GetFlaggedTweet("non-existent")
	if err != nil {
		t.Fatalf("GetFlaggedTweet should not return error: %v", err)
	}
	if notFound != nil {
		t.Error("GetFlaggedTweet should return nil for non-existent ID")
	}
}

func TestExportFlaggedTweets(t *testing.T) {
	service, cleanup := setupTestService(t)
	defer cleanup()

	// Create flagged tweets with URLs
	for i := 0; i < 5; i++ {
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
			URL:       "https://twitter.com/user/status/" + idStr,
		}
		service.repo.SaveFlaggedTweet(flagged)
	}

	// Create one without URL
	flaggedNoURL := &model.FlaggedTweet{
		ID:        "flagged-no-url",
		TweetID:   "tweet-no-url",
		JobID:     "job-1",
		Text:      "Tweet without URL",
		CreatedAt: time.Now(),
		FlaggedAt: time.Now(),
		Reason:    "Test",
		Score:     0.5,
		URL:       "",
	}
	service.repo.SaveFlaggedTweet(flaggedNoURL)

	urls, err := service.ExportFlaggedTweets("job-1")
	if err != nil {
		t.Fatalf("ExportFlaggedTweets failed: %v", err)
	}

	// Should have 5 URLs (one without URL is excluded)
	if len(urls) != 5 {
		t.Errorf("Expected 5 URLs, got %d", len(urls))
	}

	// Verify all URLs are present
	urlMap := make(map[string]bool)
	for _, url := range urls {
		urlMap[url] = true
	}

	for i := 0; i < 5; i++ {
		expectedURL := "https://twitter.com/user/status/" + strconv.Itoa(i)
		if !urlMap[expectedURL] {
			t.Errorf("Expected URL %s not found in export", expectedURL)
		}
	}
}

func TestExportFlaggedTweets_AllJobs(t *testing.T) {
	service, cleanup := setupTestService(t)
	defer cleanup()

	// Create tweets for multiple jobs
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
			URL:       "https://twitter.com/user/status/job1-" + idStr,
		}
		service.repo.SaveFlaggedTweet(flagged)
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
			URL:       "https://twitter.com/user/status/job2-" + idStr,
		}
		service.repo.SaveFlaggedTweet(flagged)
	}

	// Export all
	allURLs, err := service.ExportFlaggedTweets("")
	if err != nil {
		t.Fatalf("ExportFlaggedTweets failed: %v", err)
	}
	if len(allURLs) != 5 {
		t.Errorf("Expected 5 URLs for all jobs, got %d", len(allURLs))
	}

	// Export only job-1
	job1URLs, err := service.ExportFlaggedTweets("job-1")
	if err != nil {
		t.Fatalf("ExportFlaggedTweets failed: %v", err)
	}
	if len(job1URLs) != 3 {
		t.Errorf("Expected 3 URLs for job-1, got %d", len(job1URLs))
	}
}

func TestExportFlaggedTweets_Empty(t *testing.T) {
	service, cleanup := setupTestService(t)
	defer cleanup()

	urls, err := service.ExportFlaggedTweets("")
	if err != nil {
		t.Fatalf("ExportFlaggedTweets failed: %v", err)
	}
	if len(urls) != 0 {
		t.Errorf("Expected 0 URLs, got %d", len(urls))
	}
}
