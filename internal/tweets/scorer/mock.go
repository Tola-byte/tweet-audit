package scorer

import (
	"context"
	"math/rand"
	"strings"
	"sync"
	"time"

	"tweet-audit/internal/tweets/model"
)

// MockScorer is a test implementation that returns deterministic or random scores
// Useful for testing without API costs
type MockScorer struct {
	// Deterministic mode: if true, uses simple rules instead of randomness
	Deterministic bool
	
	// FlagProbability: probability (0-1) that a tweet will be flagged in random mode
	FlagProbability float64
	
	// Random seed for reproducible results
	seed int64
	
	// Moderation criteria
	criteria   *model.ModerationCriteria
	criteriaMu sync.RWMutex
}

// SetCriteria updates the moderation criteria for this scorer
func (m *MockScorer) SetCriteria(criteria *model.ModerationCriteria) {
	m.criteriaMu.Lock()
	defer m.criteriaMu.Unlock()
	if criteria != nil {
		m.criteria = criteria
	}
}

// NewMockScorer creates a new mock scorer
func NewMockScorer() *MockScorer {
	return &MockScorer{
		Deterministic:  false,
		FlagProbability: 0.1, // 10% of tweets flagged by default
		seed:           time.Now().UnixNano(),
	}
}

// NewDeterministicMockScorer creates a mock scorer that uses simple rules
func NewDeterministicMockScorer() *MockScorer {
	return &MockScorer{
		Deterministic:  true,
		FlagProbability: 0.0, // Not used in deterministic mode
	}
}

// Score evaluates a single tweet
func (m *MockScorer) Score(ctx context.Context, tweet *model.TweetRecord) (*model.ScoreResult, error) {
	if m.Deterministic {
		return m.deterministicScore(tweet), nil
	}
	return m.randomScore(tweet), nil
}

// ScoreBatch evaluates multiple tweets
func (m *MockScorer) ScoreBatch(ctx context.Context, tweets []*model.TweetRecord) ([]*model.ScoreResult, error) {
	results := make([]*model.ScoreResult, len(tweets))
	for i, tweet := range tweets {
		result, err := m.Score(ctx, tweet)
		if err != nil {
			return nil, err
		}
		results[i] = result
	}
	return results, nil
}

// deterministicScore uses simple rules to determine if a tweet should be flagged
// Focuses on actually abusive/harmful content, not harmless old tweets
func (m *MockScorer) deterministicScore(tweet *model.TweetRecord) *model.ScoreResult {
	text := strings.ToLower(tweet.Text)
	
	// Simple heuristics for testing - only flag actually problematic content
	shouldFlag := false
	score := 0.0
	reason := "No issues detected"
	labels := []string{}
	
	// Abusive language patterns (insults, slurs, etc.)
	abusivePatterns := []string{
		"stupid", "idiot", "moron", "retard", "dumbass", "asshole",
		"fuck you", "fuck off", "go to hell", "kill yourself", "kys",
		"you're a", "you are a", // Often precedes insults
	}
	
	for _, pattern := range abusivePatterns {
		if strings.Contains(text, pattern) {
			shouldFlag = true
			score = 0.85
			reason = "Contains abusive or offensive language"
			labels = append(labels, "abusive")
			break
		}
	}
	
	// Hate speech indicators (more specific than just "hate")
	hatePatterns := []string{
		"i hate", "i despise", "i loathe",
		"all [group] are", "every [group] is", // Generalizations
		"should die", "deserves to die", "should be killed",
	}
	
	for _, pattern := range hatePatterns {
		if strings.Contains(text, pattern) {
			shouldFlag = true
			score = 0.9
			reason = "Contains hate speech or violent language"
			labels = append(labels, "hate_speech")
			break
		}
	}
	
	// Threatening language
	threatPatterns := []string{
		"i'll kill", "i will kill", "going to kill",
		"i'll hurt", "i will hurt", "going to hurt",
		"threaten", "threatening",
	}
	
	for _, pattern := range threatPatterns {
		if strings.Contains(text, pattern) {
			shouldFlag = true
			score = 0.95
			reason = "Contains threats or violent language"
			labels = append(labels, "threatening")
			break
		}
	}
	
	// Note: We removed age-based flagging - old tweets are only flagged if they contain actual abuse
	
	return &model.ScoreResult{
		ShouldFlag: shouldFlag,
		Score:      score,
		Reason:     reason,
		Labels:     labels,
	}
}

// randomScore returns random scores based on probability
func (m *MockScorer) randomScore(tweet *model.TweetRecord) *model.ScoreResult {
	rng := rand.New(rand.NewSource(m.seed + int64(len(tweet.Text))))
	
	shouldFlag := rng.Float64() < m.FlagProbability
	score := rng.Float64()
	
	reason := "Random mock score"
	if shouldFlag {
		reason = "Mock scorer flagged this tweet (random mode)"
	}
	
	return &model.ScoreResult{
		ShouldFlag: shouldFlag,
		Score:      score,
		Reason:     reason,
		Labels:     []string{"mock"},
	}
}
