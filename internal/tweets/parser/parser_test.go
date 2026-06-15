package parser

import (
	"archive/zip"
	"bytes"
	"io"
	"strconv"
	"testing"
)

func TestParseArchive_ZIP(t *testing.T) {
	parser := NewParser()

	// Create a test ZIP file in memory
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Create tweet.js file content
	tweetJS := `window.YTD.tweets.part0 = [
	{
		"tweet": {
			"id_str": "1234567890",
			"full_text": "This is a test tweet",
			"created_at": "Mon Jan 02 15:04:05 +0000 2023",
			"entities": {
				"urls": [
					{"expanded_url": "https://example.com", "url": "https://t.co/abc"}
				],
				"hashtags": [{"text": "test"}],
				"user_mentions": [{"screen_name": "user"}],
				"media": [{"media_url": "https://example.com/image.jpg"}]
			}
		}
	},
	{
		"tweet": {
			"id_str": "0987654321",
			"full_text": "RT @user: This is a retweet",
			"created_at": "Mon Jan 02 15:04:05 +0000 2023",
			"entities": {}
		}
	}
];`

	fw, err := zw.Create("data/tweet.js")
	if err != nil {
		t.Fatalf("Failed to create file in ZIP: %v", err)
	}
	_, err = fw.Write([]byte(tweetJS))
	if err != nil {
		t.Fatalf("Failed to write to ZIP: %v", err)
	}
	zw.Close()

	// Parse the ZIP
	readerAt := bytes.NewReader(buf.Bytes())
	tweets, err := parser.ParseArchive(readerAt, int64(buf.Len()))
	if err != nil {
		t.Fatalf("Failed to parse ZIP archive: %v", err)
	}

	// Should have 1 tweet (retweet filtered out)
	if len(tweets) != 1 {
		t.Fatalf("Expected 1 tweet, got %d", len(tweets))
	}

	tweet := tweets[0]
	if tweet.ID != "1234567890" {
		t.Errorf("Expected tweet ID 1234567890, got %s", tweet.ID)
	}
	if tweet.Text != "This is a test tweet" {
		t.Errorf("Expected text 'This is a test tweet', got '%s'", tweet.Text)
	}
	if len(tweet.Links) != 1 || tweet.Links[0] != "https://example.com" {
		t.Errorf("Expected link 'https://example.com', got %v", tweet.Links)
	}
	if len(tweet.Hashtags) != 1 || tweet.Hashtags[0] != "#test" {
		t.Errorf("Expected hashtag '#test', got %v", tweet.Hashtags)
	}
	if len(tweet.Mentions) != 1 || tweet.Mentions[0] != "@user" {
		t.Errorf("Expected mention '@user', got %v", tweet.Mentions)
	}
	if len(tweet.Attachments) != 1 || tweet.Attachments[0] != "https://example.com/image.jpg" {
		t.Errorf("Expected attachment, got %v", tweet.Attachments)
	}
}

func TestParseArchive_DirectJS(t *testing.T) {
	parser := NewParser()

	// Create direct JS file content
	tweetJS := `window.YTD.tweets.part0 = [
	{
		"tweet": {
			"id_str": "1111111111",
			"full_text": "Direct JS file test",
			"created_at": "Mon Jan 02 15:04:05 +0000 2023",
			"entities": {}
		}
	}
];`

	readerAt := bytes.NewReader([]byte(tweetJS))
	tweets, err := parser.ParseArchive(readerAt, int64(len(tweetJS)))
	if err != nil {
		t.Fatalf("Failed to parse direct JS file: %v", err)
	}

	if len(tweets) != 1 {
		t.Fatalf("Expected 1 tweet, got %d", len(tweets))
	}

	if tweets[0].ID != "1111111111" {
		t.Errorf("Expected tweet ID 1111111111, got %s", tweets[0].ID)
	}
}

func TestParseArchive_RetweetFiltering(t *testing.T) {
	parser := NewParser()

	tweetJS := `window.YTD.tweets.part0 = [
	{
		"tweet": {
			"id_str": "1",
			"full_text": "Original tweet",
			"created_at": "Mon Jan 02 15:04:05 +0000 2023",
			"entities": {}
		}
	},
	{
		"tweet": {
			"id_str": "2",
			"full_text": "RT @user: This is a retweet",
			"created_at": "Mon Jan 02 15:04:05 +0000 2023",
			"entities": {}
		}
	},
	{
		"tweet": {
			"id_str": "3",
			"full_text": "RT @another: Another retweet",
			"created_at": "Mon Jan 02 15:04:05 +0000 2023",
			"entities": {}
		}
	}
];`

	readerAt := bytes.NewReader([]byte(tweetJS))
	tweets, err := parser.ParseArchive(readerAt, int64(len(tweetJS)))
	if err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	// Should only have 1 tweet (2 retweets filtered out)
	if len(tweets) != 1 {
		t.Fatalf("Expected 1 tweet, got %d", len(tweets))
	}

	if tweets[0].ID != "1" {
		t.Errorf("Expected tweet ID 1, got %s", tweets[0].ID)
	}
}

func TestIsRetweet(t *testing.T) {
	tests := []struct {
		text     string
		expected bool
	}{
		{"RT @user: Hello", true},
		{"RT @user Hello", true},
		{"  RT @user: Hello", true},
		{"Hello world", false},
		{"This is not a retweet", false},
		{"RT @", true}, // Edge case - technically starts with "RT @"
		{"RT", false},  // Just "RT" without @
	}

	for _, tt := range tests {
		result := isRetweet(tt.text)
		if result != tt.expected {
			t.Errorf("isRetweet(%q) = %v, expected %v", tt.text, result, tt.expected)
		}
	}
}

func TestExtractJSONFromJS(t *testing.T) {
	tests := []struct {
		name     string
		js       string
		expected string
	}{
		{
			name:     "window.YTD format",
			js:       `window.YTD.tweets.part0 = [{"id": 1}];`,
			expected: `[{"id": 1}]`,
		},
		{
			name:     "direct array",
			js:       `[{"id": 1}]`,
			expected: `[{"id": 1}]`,
		},
		{
			name:     "with whitespace",
			js:       `window.YTD.tweets.part0 =   [{"id": 1}];`,
			expected: `[{"id": 1}]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractJSONFromJS(tt.js)
			if result != tt.expected {
				t.Errorf("extractJSONFromJS() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestConvertToTweetRecord(t *testing.T) {
	parser := NewParser()

	raw := rawTweet{
		Tweet: struct {
			IDStr     string `json:"id_str"`
			FullText  string `json:"full_text"`
			Text      string `json:"text"`
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
		}{
			IDStr:     "123",
			FullText:  "Test tweet",
			CreatedAt: "Mon Jan 02 15:04:05 +0000 2023",
		},
	}
	raw.Tweet.Entities.URLs = []struct {
		ExpandedURL string `json:"expanded_url"`
		URL         string `json:"url"`
	}{{"https://example.com", "https://t.co/abc"}}
	raw.Tweet.Entities.Hashtags = []struct {
		Text string `json:"text"`
	}{{"test"}}
	raw.Tweet.Entities.UserMentions = []struct {
		ScreenName string `json:"screen_name"`
	}{{"user"}}
	raw.Tweet.Entities.Media = []struct {
		MediaURL string `json:"media_url"`
	}{{"https://example.com/img.jpg"}}

	result := parser.convertToTweetRecord(raw)
	if result == nil {
		t.Fatal("convertToTweetRecord returned nil")
	}

	if result.ID != "123" {
		t.Errorf("Expected ID '123', got '%s'", result.ID)
	}
	if result.Text != "Test tweet" {
		t.Errorf("Expected text 'Test tweet', got '%s'", result.Text)
	}
	if len(result.Links) != 1 || result.Links[0] != "https://example.com" {
		t.Errorf("Expected link, got %v", result.Links)
	}
}

func TestParseArchive_InvalidZIP(t *testing.T) {
	parser := NewParser()
	invalidZIP := bytes.NewReader([]byte("not a zip file"))

	_, err := parser.ParseArchive(invalidZIP, 15)
	if err == nil {
		t.Error("Expected error for invalid ZIP, got nil")
	}
}

func TestParseArchive_NoTweetFile(t *testing.T) {
	parser := NewParser()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Create a file that's not tweet.js
	fw, _ := zw.Create("data/other.js")
	fw.Write([]byte("not tweets"))
	zw.Close()

	readerAt := bytes.NewReader(buf.Bytes())
	_, err := parser.ParseArchive(readerAt, int64(buf.Len()))
	if err == nil {
		t.Error("Expected error when no tweet.js found, got nil")
	}
}

func TestParseArchive_InvalidJSON(t *testing.T) {
	parser := NewParser()

	tweetJS := `window.YTD.tweets.part0 = [invalid json];`
	readerAt := bytes.NewReader([]byte(tweetJS))

	_, err := parser.ParseArchive(readerAt, int64(len(tweetJS)))
	if err == nil {
		t.Error("Expected error for invalid JSON, got nil")
	}
}

func BenchmarkParseArchive(b *testing.B) {
	parser := NewParser()

	// Create a larger test file
	tweetJS := `window.YTD.tweets.part0 = [`
	for i := 0; i < 1000; i++ {
		if i > 0 {
			tweetJS += ","
		}
		idStr := strconv.Itoa(i)
		tweetJS += `{
			"tweet": {
				"id_str": "` + idStr + `",
				"full_text": "Tweet number ` + idStr + `",
				"created_at": "Mon Jan 02 15:04:05 +0000 2023",
				"entities": {}
			}
		}`
	}
	tweetJS += `];`

	readerAt := bytes.NewReader([]byte(tweetJS))
	size := int64(len(tweetJS))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		readerAt.Seek(0, io.SeekStart)
		_, err := parser.ParseArchive(readerAt, size)
		if err != nil {
			b.Fatalf("ParseArchive failed: %v", err)
		}
	}
}
