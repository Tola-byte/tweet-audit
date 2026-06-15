package service

import (
	"tweet-audit/internal/tweets/model"
	"tweet-audit/internal/tweets/repository"
)

// Service contains business logic for the tweets domain.
type Service struct {
	repo *repository.Repository
}

func NewService(r *repository.Repository) *Service {
	return &Service{repo: r}
}

// ListFlaggedTweets retrieves flagged tweets with pagination
// page and pageSize should be validated by handler, but we validate here as well for safety
func (s *Service) ListFlaggedTweets(jobID string, page, pageSize int) ([]*model.FlaggedTweet, int, error) {
	// Normalize pagination (handler should do this, but defense in depth)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	offset := (page - 1) * pageSize
	tweets, err := s.repo.ListFlaggedTweets(jobID, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}

	total, err := s.repo.CountFlaggedTweets(jobID)
	if err != nil {
		return nil, 0, err
	}

	return tweets, total, nil
}

// GetFlaggedTweet retrieves a specific flagged tweet by ID
func (s *Service) GetFlaggedTweet(id string) (*model.FlaggedTweet, error) {
	return s.repo.GetFlaggedTweetByID(id)
}

// ExportFlaggedTweets returns URLs for flagged tweets, optionally filtered by job
func (s *Service) ExportFlaggedTweets(jobID string) ([]string, error) {
	// Get all flagged tweets for the job (or all if jobID is empty)
	tweets, err := s.repo.ListFlaggedTweets(jobID, 10000, 0) // large limit for export
	if err != nil {
		return nil, err
	}

	urls := make([]string, 0, len(tweets))
	for _, tweet := range tweets {
		if tweet.URL != "" {
			urls = append(urls, tweet.URL)
		}
	}

	return urls, nil
}
