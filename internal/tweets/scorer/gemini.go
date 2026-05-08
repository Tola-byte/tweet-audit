package scorer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"tweet-audit/internal/config"
	"tweet-audit/internal/tweets/logger"
	"tweet-audit/internal/tweets/model"
)

// GeminiScorer uses Google's Gemini API to evaluate tweets
// Production-ready with rate limiting, circuit breaker, and concurrency control
type GeminiScorer struct {
	apiKey         string
	client         *http.Client
	model          string
	apiURL         string
	rateLimiter    *RateLimiter    // Rate limiting (15 req/min for gemini-2.5-flash-lite)
	circuitBreaker *CircuitBreaker // Circuit breaker pattern
	semaphore      chan struct{}   // Concurrency control (max 10 concurrent)
	maxRetries     int             // Retry configuration
	retryBackoff   time.Duration
	criteria       *model.ModerationCriteria // Moderation criteria/alignment rules
	criteriaMu     sync.RWMutex              // Protect criteria updates
}

// NewGeminiScorer creates a new Gemini-based scorer with production patterns
func NewGeminiScorer(apiKey string) (*GeminiScorer, error) {
	cfg := config.GeminiConfig{
		APIKey:          apiKey,
		Model:           "gemini-2.5-flash-lite",
		RateLimitPerMin: 15,
		MaxRetries:      3,
		RetryBackoff:    1 * time.Second,
		Timeout:         30 * time.Second,
		CircuitBreaker: config.CircuitBreakerConfig{
			FailureThreshold: 5,
			SuccessThreshold: 2,
			OpenTimeout:      30 * time.Second,
			HalfOpenTimeout:  10 * time.Second,
		},
	}
	return NewGeminiScorerWithConfig(cfg)
}

// NewGeminiScorerWithConfig creates a new Gemini-based scorer with config
func NewGeminiScorerWithConfig(cfg config.GeminiConfig) (*GeminiScorer, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY is required")
	}

	rateLimiter := NewRateLimiter(cfg.RateLimitPerMin, time.Minute)
	circuitBreaker := NewCircuitBreaker(
		cfg.CircuitBreaker.FailureThreshold,
		cfg.CircuitBreaker.SuccessThreshold,
		cfg.CircuitBreaker.OpenTimeout,
		cfg.CircuitBreaker.HalfOpenTimeout,
	)

	apiURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent", cfg.Model)

	return &GeminiScorer{
		apiKey: cfg.APIKey,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
		model:          cfg.Model,
		apiURL:         apiURL,
		rateLimiter:    rateLimiter,
		circuitBreaker: circuitBreaker,
		semaphore:      make(chan struct{}, 10),
		maxRetries:     cfg.MaxRetries,
		retryBackoff:   cfg.RetryBackoff,
		criteria:       model.DefaultCriteria(),
	}, nil
}

// SetCriteria updates the moderation criteria for this scorer
func (g *GeminiScorer) SetCriteria(criteria *model.ModerationCriteria) {
	g.criteriaMu.Lock()
	defer g.criteriaMu.Unlock()
	if criteria != nil {
		g.criteria = criteria
	}
}

// Score evaluates a single tweet using Gemini with retry, rate limiting, and circuit breaker
func (g *GeminiScorer) Score(ctx context.Context, tweet *model.TweetRecord) (*model.ScoreResult, error) {
	prompt := g.buildPrompt(tweet)

	var lastErr error
	backoff := g.retryBackoff
	rateLimitBackoff := 60 * time.Second

	for attempt := 0; attempt <= g.maxRetries; attempt++ {
		var result *model.ScoreResult

		err := g.circuitBreaker.CallWithCriticalError(ctx, func() error {
			if err := g.rateLimiter.Wait(ctx); err != nil {
				logger.WithFields(map[string]interface{}{
					"tweet_id": tweet.ID,
					"attempt":  attempt,
					"error":    err.Error(),
				}).Error("Score: rate limiter blocked — context cancelled or timed out before acquiring token")
				return err
			}

			select {
			case g.semaphore <- struct{}{}:
				logger.WithFields(map[string]interface{}{
					"tweet_id":      tweet.ID,
					"attempt":       attempt,
					"semaphore_len": len(g.semaphore),
					"semaphore_cap": cap(g.semaphore),
				}).Debug("Score: semaphore acquired, making API call")
				defer func() { <-g.semaphore }()
			case <-ctx.Done():
				logger.WithFields(map[string]interface{}{
					"tweet_id": tweet.ID,
					"attempt":  attempt,
					"error":    ctx.Err().Error(),
				}).Error("Score: context cancelled waiting for semaphore — API call never made")
				return ctx.Err()
			}

			var apiErr error
			result, apiErr = g.makeAPICall(ctx, tweet, prompt)
			if apiErr != nil {
				logger.WithFields(map[string]interface{}{
					"tweet_id": tweet.ID,
					"attempt":  attempt,
					"error":    apiErr.Error(),
				}).Error("Score: API call failed")
			} else {
				logger.WithFields(map[string]interface{}{
					"tweet_id":    tweet.ID,
					"attempt":     attempt,
					"should_flag": result.ShouldFlag,
					"score":       result.Score,
				}).Debug("Score: API call succeeded")
			}
			return apiErr
		}, func(err error) bool {
			if httpErr, ok := err.(*HTTPError); ok {
				return httpErr.StatusCode == 401 || httpErr.StatusCode == 404 || httpErr.StatusCode == 403
			}
			return false
		})

		if err != nil {
			lastErr = err

			if err == ErrCircuitOpen {
				logger.WithFields(map[string]interface{}{
					"tweet_id": tweet.ID,
				}).Warn("Circuit breaker is open - skipping Gemini API call. Check logs above for the actual API error that caused this (look for 'Gemini API returned error' messages). Common causes: 401=invalid API key, 404=wrong model name, 429=rate limit exceeded. Circuit will attempt recovery after 30s timeout.")
				return nil, err
			}

			if err == context.Canceled || err == context.DeadlineExceeded {
				return nil, err
			}

			if httpErr, ok := err.(*HTTPError); ok && httpErr.StatusCode >= 400 && httpErr.StatusCode < 500 && httpErr.StatusCode != 429 {
				logger.WithFields(map[string]interface{}{
					"tweet_id":    tweet.ID,
					"status_code": httpErr.StatusCode,
					"error":       httpErr.Body,
				}).Error("Permanent error from Gemini API - not retrying")
				return nil, err
			}

			logger.WithFields(map[string]interface{}{
				"tweet_id": tweet.ID,
				"attempt":  attempt + 1,
				"error":    err.Error(),
			}).Warn("Retryable error from Gemini API: %v", err)

			if attempt < g.maxRetries {
				var retryDelay time.Duration
				if httpErr, ok := err.(*HTTPError); ok && httpErr.StatusCode == 429 {
					if strings.Contains(httpErr.Body, "RESOURCE_EXHAUSTED") {
						logger.WithFields(map[string]interface{}{
							"tweet_id": tweet.ID,
						}).Error("Daily quota exhausted — will pause until midnight UTC")
						return nil, ErrDailyQuotaExceeded
					}
					retryDelay = rateLimitBackoff
					rateLimitBackoff *= 2
					if rateLimitBackoff > 5*time.Minute {
						rateLimitBackoff = 5 * time.Minute
					}
					logger.WithFields(map[string]interface{}{
						"tweet_id":      tweet.ID,
						"attempt":       attempt + 1,
						"retry_delay_s": retryDelay.Seconds(),
						"next_delay_s":  rateLimitBackoff.Seconds(),
					}).Warn("Rate limit exceeded (429) - backing off before retry")
				} else {
					// Other retryable errors: exponential backoff
					retryDelay = backoff
					backoff *= 2
					logger.WithFields(map[string]interface{}{
						"tweet_id": tweet.ID,
						"attempt":  attempt + 1,
						"error":    err.Error(),
					}).Debug("Retrying Gemini API call")
				}

				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(retryDelay):
					// Continue to retry
				}
				continue
			}

			return nil, err
		}

		// Success - return result
		return result, nil
	}

	return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

// ErrDailyQuotaExceeded is returned when Gemini's daily free-tier quota is exhausted.
// Unlike a burst 429, retrying won't help — the quota resets at midnight UTC.
var ErrDailyQuotaExceeded = fmt.Errorf("gemini daily quota exceeded — quota resets at midnight UTC")

// HTTPError represents an HTTP error from Gemini API
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}

// isRetryableError checks if an error should be retried
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if err == context.Canceled || err == context.DeadlineExceeded {
		return false
	}
	if httpErr, ok := err.(*HTTPError); ok {
		// Retry on 429 (rate limit) and 5xx (server errors)
		return httpErr.StatusCode == 429 || httpErr.StatusCode >= 500
	}
	// Retry on network errors
	return true
}

// makeAPICall performs the actual HTTP request to Gemini
func (g *GeminiScorer) makeAPICall(ctx context.Context, tweet *model.TweetRecord, prompt string) (*model.ScoreResult, error) {
	logger.WithFields(map[string]interface{}{
		"tweet_id": tweet.ID,
		"model":    g.model,
	}).Debug("Calling Gemini API")

	// Prepare request
	requestBody := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]interface{}{
					{"text": prompt},
				},
			},
		},
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", g.apiURL+"?key="+g.apiKey, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Make API call
	start := time.Now()
	resp, err := g.client.Do(req)
	duration := time.Since(start)

	if err != nil {
		logger.WithFields(map[string]interface{}{
			"tweet_id": tweet.ID,
			"error":    err.Error(),
			"duration": duration,
		}).Error("Gemini API call failed: %v", err)

		// Check if it's a network/timeout error
		if err == context.DeadlineExceeded {
			return nil, fmt.Errorf("gemini API timeout: request took longer than 30s")
		}

		return nil, fmt.Errorf("gemini API error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)

		// Provide helpful error messages for common issues
		var errorMsg string
		var isCritical bool
		switch resp.StatusCode {
		case 400:
			errorMsg = fmt.Sprintf("Bad request (400) - Check API key format and request. Response: %s", bodyStr)
		case 401:
			errorMsg = "Unauthorized (401) - INVALID API KEY. Check your GEMINI_API_KEY in .env file"
			isCritical = true
		case 403:
			errorMsg = fmt.Sprintf("Forbidden (403) - API key may not have access to model '%s'. Response: %s", g.model, bodyStr)
			isCritical = true
		case 404:
			errorMsg = fmt.Sprintf("Not found (404) - Model '%s' not found. Check if model name is correct. Response: %s", g.model, bodyStr)
			isCritical = true
		case 429:
			errorMsg = "Rate limit exceeded (429) - Too many requests. Pausing for 60 seconds before retry."
			// 429 is not critical (we'll retry), but we should pause longer
			// The retry logic will handle the backoff
		case 500, 502, 503, 504:
			errorMsg = fmt.Sprintf("Server error (%d) - Gemini API is temporarily unavailable. Response: %s", resp.StatusCode, bodyStr)
		default:
			errorMsg = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, bodyStr)
		}

		// Log critical errors MULTIPLE TIMES to ensure visibility
		if isCritical {
			// Print a banner to make it super visible
			logger.Error("")
			logger.Error("═══════════════════════════════════════════════════════════════")
			logger.Error("CRITICAL GEMINI API ERROR - ACTION REQUIRED")
			logger.Error("═══════════════════════════════════════════════════════════════")
			logger.Error("Status Code: %d", resp.StatusCode)
			logger.Error("Error: %s", errorMsg)
			logger.Error("Model: %s", g.model)
			logger.Error("API URL: %s", g.apiURL)
			logger.Error("Response Body: %s", bodyStr)
			logger.Error("═══════════════════════════════════════════════════════════════")
			logger.Error("")
		}

		// Also log in structured format for debugging
		logger.WithFields(map[string]interface{}{
			"tweet_id":    tweet.ID,
			"status_code": resp.StatusCode,
			"response":    bodyStr,
			"api_url":     g.apiURL,
			"model":       g.model,
			"is_critical": isCritical,
		}).Error("Gemini API returned error: HTTP %d - %s", resp.StatusCode, errorMsg)

		return nil, &HTTPError{
			StatusCode: resp.StatusCode,
			Body:       errorMsg,
		}
	}

	// Parse response
	var geminiResponse struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&geminiResponse); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(geminiResponse.Candidates) == 0 || len(geminiResponse.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("empty response from Gemini")
	}

	responseText := geminiResponse.Candidates[0].Content.Parts[0].Text

	logger.WithFields(map[string]interface{}{
		"tweet_id": tweet.ID,
		"duration": duration,
	}).Debug("Gemini API response received")

	// Parse Gemini's JSON response
	return g.parseResponse(responseText)
}

// ScoreBatch evaluates multiple tweets efficiently
func (g *GeminiScorer) ScoreBatch(ctx context.Context, tweets []*model.TweetRecord) ([]*model.ScoreResult, error) {
	results := make([]*model.ScoreResult, len(tweets))
	for i, tweet := range tweets {
		result, err := g.Score(ctx, tweet)
		if err != nil {
			// Continue processing other tweets even if one fails
			results[i] = &model.ScoreResult{
				ShouldFlag: false,
				Score:      0.0,
				Reason:     fmt.Sprintf("Error: %v", err),
			}
			continue
		}
		results[i] = result
	}
	return results, nil
}

// buildPrompt creates a prompt for Gemini to evaluate the tweet using moderation criteria
func (g *GeminiScorer) buildPrompt(tweet *model.TweetRecord) string {
	g.criteriaMu.RLock()
	criteria := g.criteria
	g.criteriaMu.RUnlock()

	// Use default if nil
	if criteria == nil {
		criteria = model.DefaultCriteria()
	}

	dateStr := tweet.CreatedAt.Format("January 2, 2006")

	// Escape JSON special characters in tweet text
	safeText := strings.ReplaceAll(tweet.Text, `"`, `\"`)
	safeText = strings.ReplaceAll(safeText, "\n", " ")
	safeText = strings.ReplaceAll(safeText, "\r", " ")

	// Build criteria instructions
	var flagConditions []string
	var excludeConditions []string

	if criteria.ExcludeThreats {
		flagConditions = append(flagConditions, "- Threats of violence or harm")
	}
	if criteria.ExcludeHateSpeech {
		flagConditions = append(flagConditions, "- Hate speech or discrimination targeting groups")
	}
	if criteria.ExcludeHarassment {
		flagConditions = append(flagConditions, "- Harassment or targeted abuse")
	}
	if criteria.ExcludeProfanity {
		flagConditions = append(flagConditions, "- Severe profanity used aggressively")
	}
	if criteria.ProfessionalCheck {
		flagConditions = append(flagConditions, "- Unprofessional language or tone")
	}
	if criteria.ExcludePolitics {
		flagConditions = append(flagConditions, "- Political content or statements")
	}
	if len(criteria.ForbiddenWords) > 0 {
		forbiddenList := strings.Join(criteria.ForbiddenWords, ", ")
		flagConditions = append(flagConditions, fmt.Sprintf("- Contains forbidden words/phrases: %s", forbiddenList))
	}

	// Build tone requirement
	toneInstruction := ""
	if criteria.Tone != "" {
		toneInstruction = fmt.Sprintf("\n4. Expected tone: %s. Flag tweets that significantly deviate from this tone.", criteria.Tone)
	}

	// Build DO NOT flag conditions
	excludeConditions = []string{
		"- Simply old (age alone is not a reason)",
		"- Contain mild language or jokes",
		"- Express opinions you disagree with (unless they violate other criteria)",
		"- Are sarcastic or self-deprecating",
	}
	if !criteria.ExcludeProfanity {
		excludeConditions = append(excludeConditions, "- Use profanity in non-aggressive contexts")
	}
	if !criteria.ExcludePolitics {
		excludeConditions = append(excludeConditions, "- Express political opinions (unless they violate other criteria)")
	}

	flagSection := "None specified"
	if len(flagConditions) > 0 {
		flagSection = strings.Join(flagConditions, "\n   ")
	}

	excludeSection := strings.Join(excludeConditions, "\n   ")

	return fmt.Sprintf(`You are a content moderation assistant. Evaluate this tweet based on the provided criteria.

Tweet posted on: %s
Tweet text: "%s"

MODERATION CRITERIA:
Flag tweets that contain:
   %s

DO NOT flag tweets that are:
   %s
%s

3. Consider context - a tweet saying "I hate Mondays" is NOT hate speech unless it targets a specific group.

Examples:
Tweet: "I hate Mondays"
Response: {"should_flag": false, "score": 0.0, "reason": "Not hate speech, just expressing frustration", "labels": []}

Tweet: "All [group] should die"
Response: {"should_flag": true, "score": 0.95, "reason": "Hate speech targeting a group", "labels": ["hate_speech"]}

Now evaluate the tweet above based on the criteria. Respond ONLY with valid JSON, no other text:
{
  "should_flag": true/false,
  "score": 0.0-1.0,
  "reason": "brief explanation",
  "labels": ["abusive", "threatening", etc.]
}`, dateStr, safeText, flagSection, excludeSection, toneInstruction)
}

// parseResponse parses Gemini's JSON response
func (g *GeminiScorer) parseResponse(response string) (*model.ScoreResult, error) {
	// Try to extract JSON from response (Gemini might add extra text)
	start := strings.Index(response, "{")
	end := strings.LastIndex(response, "}")
	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("invalid response format: %s", response)
	}

	jsonStr := response[start : end+1]

	var result struct {
		ShouldFlag bool     `json:"should_flag"`
		Score      float64  `json:"score"`
		Reason     string   `json:"reason"`
		Labels     []string `json:"labels"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	// Validate score is in range
	if result.Score < 0 {
		result.Score = 0
	}
	if result.Score > 1 {
		result.Score = 1
	}

	return &model.ScoreResult{
		ShouldFlag: result.ShouldFlag,
		Score:      result.Score,
		Reason:     result.Reason,
		Labels:     result.Labels,
	}, nil
}
