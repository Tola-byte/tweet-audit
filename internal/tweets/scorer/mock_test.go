package scorer

import (
	"context"
	"strconv"
	"testing"

	"tweet-audit/internal/tweets/model"
)

func TestMockScorer_Deterministic(t *testing.T) {
	scorer := NewDeterministicMockScorer()

	tests := []struct {
		name     string
		text     string
		shouldFlag bool
	}{
		{
			name:      "abusive language",
			text:      "You're a stupid idiot",
			shouldFlag: true,
		},
		{
			name:      "threat",
			text:      "I'll kill you",
			shouldFlag: true,
		},
		{
			name:      "hate speech",
			text:      "I hate all of them",
			shouldFlag: true,
		},
		{
			name:      "harmless tweet",
			text:      "Just had a great day at the beach!",
			shouldFlag: false,
		},
		{
			name:      "normal conversation",
			text:      "What's the weather like today?",
			shouldFlag: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tweet := &model.TweetRecord{
				ID:   "test-tweet",
				Text: tt.text,
			}

			result, err := scorer.Score(context.Background(), tweet)
			if err != nil {
				t.Fatalf("Score failed: %v", err)
			}

			if result.ShouldFlag != tt.shouldFlag {
				t.Errorf("Expected ShouldFlag=%v, got %v", tt.shouldFlag, result.ShouldFlag)
			}

			if tt.shouldFlag && result.Score < 0.5 {
				t.Errorf("Flagged tweet should have score >= 0.5, got %f", result.Score)
			}

			if tt.shouldFlag && result.Reason == "" {
				t.Error("Flagged tweet should have a reason")
			}
		})
	}
}

func TestMockScorer_Random(t *testing.T) {
	scorer := NewMockScorer()
	scorer.FlagProbability = 0.5

	// Run multiple times with different tweet texts to check randomness
	// The random scorer uses text length in seed, so we need different texts
	flaggedCount := 0
	iterations := 100
	allSame := true
	firstResult := false
	
	for i := 0; i < iterations; i++ {
		tweet := &model.TweetRecord{
			ID:   "test-tweet-" + strconv.Itoa(i),
			Text: "Random test tweet " + strconv.Itoa(i) + " with varying length " + strconv.Itoa(i*7),
		}
		result, err := scorer.Score(context.Background(), tweet)
		if err != nil {
			t.Fatalf("Score failed: %v", err)
		}
		if result.ShouldFlag {
			flaggedCount++
		}
		if i == 0 {
			firstResult = result.ShouldFlag
		} else if result.ShouldFlag != firstResult {
			allSame = false
		}
	}

	// Test that we get some variation (not all the same)
	if allSame {
		t.Error("Random scorer should produce varied results, but all results were the same")
	}

	// With 50% probability, we should get some flags
	// The seed-based randomness might not be perfectly uniform, so we just check
	// that it's not all flagged or all unflagged (some variation exists)
	if flaggedCount == 0 {
		t.Error("Random scorer with 50% probability should flag at least some tweets")
	}
	if flaggedCount == iterations {
		t.Error("Random scorer with 50% probability should not flag all tweets")
	}
}

func TestMockScorer_SetCriteria(t *testing.T) {
	scorer := NewMockScorer()

	criteria := &model.ModerationCriteria{
		ForbiddenWords:  []string{"crypto", "NFT"},
		ProfessionalCheck: true,
		ExcludePolitics:    true,
	}

	scorer.SetCriteria(criteria)

	// Criteria is set but not used in mock scorer currently
	// This test just ensures SetCriteria doesn't panic
	tweet := &model.TweetRecord{
		ID:   "test",
		Text: "Test tweet",
	}

	_, err := scorer.Score(context.Background(), tweet)
	if err != nil {
		t.Fatalf("Score failed: %v", err)
	}
}

func TestMockScorer_ScoreBatch(t *testing.T) {
	scorer := NewDeterministicMockScorer()

	tweets := []*model.TweetRecord{
		{ID: "1", Text: "You're stupid"},
		{ID: "2", Text: "Nice weather today"},
		{ID: "3", Text: "I'll kill you"},
	}

	results, err := scorer.ScoreBatch(context.Background(), tweets)
	if err != nil {
		t.Fatalf("ScoreBatch failed: %v", err)
	}

	if len(results) != len(tweets) {
		t.Fatalf("Expected %d results, got %d", len(tweets), len(results))
	}

	// First tweet should be flagged (abusive)
	if !results[0].ShouldFlag {
		t.Error("Expected first tweet to be flagged")
	}

	// Second tweet should not be flagged
	if results[1].ShouldFlag {
		t.Error("Expected second tweet not to be flagged")
	}

	// Third tweet should be flagged (threat)
	if !results[2].ShouldFlag {
		t.Error("Expected third tweet to be flagged")
	}
}

func TestMockScorer_AbusivePatterns(t *testing.T) {
	scorer := NewDeterministicMockScorer()

	abusiveTweets := []string{
		"You're a moron",
		"Go to hell",
		"Kill yourself",
		"Fuck off",
		"You are a dumbass",
	}

	for _, text := range abusiveTweets {
		tweet := &model.TweetRecord{ID: "test", Text: text}
		result, err := scorer.Score(context.Background(), tweet)
		if err != nil {
			t.Fatalf("Score failed: %v", err)
		}
		if !result.ShouldFlag {
			t.Errorf("Expected tweet '%s' to be flagged", text)
		}
		if result.Score < 0.8 {
			t.Errorf("Expected score >= 0.8 for abusive tweet, got %f", result.Score)
		}
	}
}

func TestMockScorer_ThreatPatterns(t *testing.T) {
	scorer := NewDeterministicMockScorer()

	threatTweets := []string{
		"I'll kill you",
		"I will hurt you",
		"Going to kill them",
		"Threatening behavior",
	}

	for _, text := range threatTweets {
		tweet := &model.TweetRecord{ID: "test", Text: text}
		result, err := scorer.Score(context.Background(), tweet)
		if err != nil {
			t.Fatalf("Score failed: %v", err)
		}
		if !result.ShouldFlag {
			t.Errorf("Expected tweet '%s' to be flagged", text)
		}
		if result.Score < 0.9 {
			t.Errorf("Expected score >= 0.9 for threat, got %f", result.Score)
		}
	}
}

func TestMockScorer_HarmlessTweets(t *testing.T) {
	scorer := NewDeterministicMockScorer()

	harmlessTweets := []string{
		"Just had lunch",
		"Beautiful sunset today",
		"Working on a new project",
		"Happy Friday everyone!",
		"Thanks for the support",
	}

	for _, text := range harmlessTweets {
		tweet := &model.TweetRecord{ID: "test", Text: text}
		result, err := scorer.Score(context.Background(), tweet)
		if err != nil {
			t.Fatalf("Score failed: %v", err)
		}
		if result.ShouldFlag {
			t.Errorf("Expected tweet '%s' not to be flagged", text)
		}
		if result.Score > 0.1 {
			t.Errorf("Expected score <= 0.1 for harmless tweet, got %f", result.Score)
		}
	}
}
