package util

import "fmt"

// GetTweetURL generates the X/Twitter URL for a tweet
func GetTweetURL(tweetID string) string {
	return fmt.Sprintf("https://twitter.com/i/web/status/%s", tweetID)
}
