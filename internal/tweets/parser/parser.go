package parser

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"tweet-audit/internal/tweets/logger"
	"tweet-audit/internal/tweets/model"
	"tweet-audit/internal/tweets/util"
)

// Parser handles extraction and parsing of X/Twitter archive files
type Parser struct{}

func NewParser() *Parser {
	return &Parser{}
}

// ParseArchive extracts and parses tweets from either a ZIP archive or a direct JS file
// It auto-detects the format by checking for ZIP magic bytes
func (p *Parser) ParseArchive(r io.ReaderAt, size int64) ([]*model.TweetRecord, error) {
	logger.Debug("Detecting file format (checking magic bytes)")
	header := make([]byte, 4)
	if _, err := r.ReadAt(header, 0); err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to read file header: %w", err)
	}

	isZIP := len(header) >= 4 && header[0] == 'P' && header[1] == 'K' && header[2] == 0x03 && header[3] == 0x04

	if isZIP {
		logger.Info("Detected ZIP archive format")
		return p.parseZIPArchive(r, size)
	}

	logger.Info("Detected direct JS file format")
	return p.parseDirectJSFile(r, size)
}

// parseZIPArchive handles ZIP file archives (original behavior)
func (p *Parser) parseZIPArchive(r io.ReaderAt, size int64) ([]*model.TweetRecord, error) {
	logger.Debug("Opening ZIP archive")
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("failed to open ZIP archive: %w", err)
	}

	logger.Debug("Searching for tweet.js or tweets.js in ZIP archive (%d files)", len(zr.File))
	var tweetFile *zip.File
	for _, f := range zr.File {
		name := strings.ToLower(f.Name)
		if strings.Contains(name, "tweet") && strings.HasSuffix(name, ".js") {
			tweetFile = f
			logger.Info("Found tweet file in archive: %s", f.Name)
			break
		}
	}

	if tweetFile == nil {
		logger.Error("No tweet.js or tweets.js file found in ZIP archive")
		return nil, fmt.Errorf("no tweet.js or tweets.js file found in ZIP archive")
	}

	logger.Debug("Reading tweet file from ZIP")
	// Read the tweet file
	rc, err := tweetFile.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open tweet file: %w", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("failed to read tweet file: %w", err)
	}

	logger.Debug("Tweet file size: %d bytes", len(data))
	return p.parseJSContent(data)
}

// parseDirectJSFile handles direct JS file uploads (unzipped)
func (p *Parser) parseDirectJSFile(r io.ReaderAt, size int64) ([]*model.TweetRecord, error) {
	logger.Debug("Reading direct JS file (%d bytes)", size)
	// Read entire file into memory
	// For unzipped files, this should be much smaller (typically 10-50MB)
	data := make([]byte, size)
	n, err := r.ReadAt(data, 0)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to read JS file: %w", err)
	}
	data = data[:n]

	logger.Debug("JS file read: %d bytes", n)
	return p.parseJSContent(data)
}

// parseJSContent extracts JSON from JS wrapper and converts to TweetRecord
func (p *Parser) parseJSContent(data []byte) ([]*model.TweetRecord, error) {
	logger.Debug("Extracting JSON from JS wrapper")
	// Extract JSON from JS wrapper (X archives wrap JSON in JS like: window.YTD.tweets.part0 = [...])
	jsonData := extractJSONFromJS(string(data))
	if jsonData == "" {
		logger.Error("Could not extract JSON from JS wrapper")
		return nil, fmt.Errorf("could not extract JSON from JS wrapper - file may not be a valid tweet.js file")
	}

	logger.Debug("Parsing JSON array (size: %d bytes)", len(jsonData))
	// Parse JSON array
	var rawTweets []rawTweet
	if err := json.Unmarshal([]byte(jsonData), &rawTweets); err != nil {
		logger.Error("Failed to parse JSON: %v", err)
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	logger.Debug("Converting %d raw tweets to TweetRecord", len(rawTweets))
	// Convert to TweetRecord, filtering out retweets
	tweets := make([]*model.TweetRecord, 0, len(rawTweets))
	retweetCount := 0
	for _, rt := range rawTweets {
		// Skip retweets (text starts with "RT @")
		text := rt.Tweet.FullText
		if text == "" {
			text = rt.Tweet.Text
		}
		if isRetweet(text) {
			retweetCount++
			continue
		}

		tr := p.convertToTweetRecord(rt)
		if tr != nil {
			tweets = append(tweets, tr)
		}
	}

	logger.Info("Successfully parsed %d original tweets (filtered out %d retweets)", len(tweets), retweetCount)
	return tweets, nil
}

// extractJSONFromJS removes the JS wrapper and extracts the JSON array
// Handles formats like: window.YTD.tweets.part0 = [...] or just [...]
func extractJSONFromJS(jsContent string) string {
	// Try to find JSON array pattern
	// Match: window.YTD.tweets.part0 = [...] or similar
	re := regexp.MustCompile(`=\s*(\[.*\])`)
	matches := re.FindStringSubmatch(jsContent)
	if len(matches) > 1 {
		return matches[1]
	}

	// Fallback: try to find array directly
	start := strings.Index(jsContent, "[")
	end := strings.LastIndex(jsContent, "]")
	if start != -1 && end != -1 && end > start {
		return jsContent[start : end+1]
	}

	return ""
}

// rawTweet represents the structure from X archive JSON
type rawTweet struct {
	Tweet struct {
		IDStr     string `json:"id_str"`
		FullText  string `json:"full_text"`
		Text      string `json:"text"` // fallback if full_text not present
		CreatedAt string `json:"created_at"`
		Entities  struct {
			URLs []struct {
				ExpandedURL string `json:"expanded_url"`
				URL         string `json:"url"`
			} `json:"urls"`
			Hashtags []struct {
				Text string `json:"text"`
			} `json:"hashtags"`
			UserMentions []struct {
				ScreenName string `json:"screen_name"`
			} `json:"user_mentions"`
			Media []struct {
				MediaURL string `json:"media_url"`
			} `json:"media"`
		} `json:"entities"`
	} `json:"tweet"`
}

// convertToTweetRecord converts raw archive tweet to our domain model
func (p *Parser) convertToTweetRecord(rt rawTweet) *model.TweetRecord {
	tweet := rt.Tweet

	// Get text (prefer full_text, fallback to text)
	text := tweet.FullText
	if text == "" {
		text = tweet.Text
	}

	// Parse created_at timestamp
	// X format: "Mon Jan 02 15:04:05 +0000 2006"
	createdAt, err := time.Parse("Mon Jan 02 15:04:05 +0000 2006", tweet.CreatedAt)
	if err != nil {
		// If parsing fails, use current time as fallback
		createdAt = time.Now()
	}

	// Extract links
	links := make([]string, 0)
	for _, url := range tweet.Entities.URLs {
		if url.ExpandedURL != "" {
			links = append(links, url.ExpandedURL)
		} else if url.URL != "" {
			links = append(links, url.URL)
		}
	}

	// Extract hashtags
	hashtags := make([]string, 0, len(tweet.Entities.Hashtags))
	for _, h := range tweet.Entities.Hashtags {
		if h.Text != "" {
			hashtags = append(hashtags, "#"+h.Text)
		}
	}

	// Extract mentions
	mentions := make([]string, 0, len(tweet.Entities.UserMentions))
	for _, m := range tweet.Entities.UserMentions {
		if m.ScreenName != "" {
			mentions = append(mentions, "@"+m.ScreenName)
		}
	}

	// Extract media attachments
	attachments := make([]string, 0, len(tweet.Entities.Media))
	for _, m := range tweet.Entities.Media {
		if m.MediaURL != "" {
			attachments = append(attachments, m.MediaURL)
		}
	}

	return &model.TweetRecord{
		ID:          tweet.IDStr,
		Text:        text,
		CreatedAt:   createdAt,
		Links:       links,
		Hashtags:    hashtags,
		Mentions:    mentions,
		Attachments: attachments,
	}
}

// isRetweet checks if a tweet is a retweet (starts with "RT @")
func isRetweet(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "RT @")
}

// GetTweetURL is deprecated - use util.GetTweetURL instead
// Kept for backward compatibility
func GetTweetURL(tweetID string) string {
	return util.GetTweetURL(tweetID)
}
