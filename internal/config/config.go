package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Server ServerConfig
	Database DatabaseConfig
	Storage StorageConfig
	Gemini GeminiConfig
	Worker WorkerConfig
}

type ServerConfig struct {
	Port         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

type DatabaseConfig struct {
	Path        string
	BusyTimeout time.Duration
}

type StorageConfig struct {
	UploadDir      string
	MaxUploadSize  int64
}

type GeminiConfig struct {
	APIKey            string
	Model             string
	RateLimitPerMin   int
	MaxRetries        int
	RetryBackoff      time.Duration
	Timeout           time.Duration
	CircuitBreaker    CircuitBreakerConfig
}

type CircuitBreakerConfig struct {
	FailureThreshold int
	SuccessThreshold int
	OpenTimeout      time.Duration
	HalfOpenTimeout  time.Duration
}

type WorkerConfig struct {
	JobQueueSize     int
	TweetQueueSize   int
	DailyQuotaLimit int
	TweetBatchSize   int
	JobUpdateInterval time.Duration
}

func Load() (*Config, error) {
	cfg := &Config{
		Server: ServerConfig{
			Port:         getEnv("SERVER_PORT", "8080"),
			ReadTimeout:  getDurationEnv("SERVER_READ_TIMEOUT", 30*time.Second),
			WriteTimeout: getDurationEnv("SERVER_WRITE_TIMEOUT", 30*time.Second),
			IdleTimeout:  getDurationEnv("SERVER_IDLE_TIMEOUT", 120*time.Second),
		},
		Database: DatabaseConfig{
			Path:        getEnv("DATABASE_PATH", "data/tweet-audit.db"),
			BusyTimeout: getDurationEnv("DATABASE_BUSY_TIMEOUT", 5*time.Second),
		},
		Storage: StorageConfig{
			UploadDir:     getEnv("STORAGE_UPLOAD_DIR", "data/uploads"),
			MaxUploadSize: getInt64Env("STORAGE_MAX_UPLOAD_SIZE", 500<<20), // 500MB default
		},
		Gemini: GeminiConfig{
			APIKey:          getEnv("GEMINI_API_KEY", ""),
			Model:           getEnv("GEMINI_MODEL", "gemini-2.5-flash-lite"),
			RateLimitPerMin: getIntEnv("GEMINI_RATE_LIMIT_PER_MIN", 15),
			MaxRetries:      getIntEnv("GEMINI_MAX_RETRIES", 3),
			RetryBackoff:    getDurationEnv("GEMINI_RETRY_BACKOFF", 1*time.Second),
			Timeout:         getDurationEnv("GEMINI_TIMEOUT", 30*time.Second),
			CircuitBreaker: CircuitBreakerConfig{
				FailureThreshold: getIntEnv("GEMINI_CB_FAILURE_THRESHOLD", 5),
				SuccessThreshold: getIntEnv("GEMINI_CB_SUCCESS_THRESHOLD", 2),
				OpenTimeout:      getDurationEnv("GEMINI_CB_OPEN_TIMEOUT", 30*time.Second),
				HalfOpenTimeout:  getDurationEnv("GEMINI_CB_HALF_OPEN_TIMEOUT", 10*time.Second),
			},
		},
		Worker: WorkerConfig{
			JobQueueSize:      getIntEnv("WORKER_JOB_QUEUE_SIZE", 100),
			TweetQueueSize:    getIntEnv("WORKER_TWEET_QUEUE_SIZE", 1000),
			DailyQuotaLimit:   getIntEnv("WORKER_DAILY_QUOTA_LIMIT", 10000000),
			TweetBatchSize:    getIntEnv("WORKER_TWEET_BATCH_SIZE", 50),
			JobUpdateInterval: getDurationEnv("WORKER_JOB_UPDATE_INTERVAL", 5*time.Second),
		},
	}

	return cfg, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getIntEnv(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getInt64Env(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.ParseInt(value, 10, 64); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getDurationEnv(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}

func (c *Config) Validate() error {
	if c.Server.Port == "" {
		return fmt.Errorf("SERVER_PORT cannot be empty")
	}
	if c.Database.Path == "" {
		return fmt.Errorf("DATABASE_PATH cannot be empty")
	}
	if c.Storage.UploadDir == "" {
		return fmt.Errorf("STORAGE_UPLOAD_DIR cannot be empty")
	}
	if c.Storage.MaxUploadSize <= 0 {
		return fmt.Errorf("STORAGE_MAX_UPLOAD_SIZE must be positive")
	}
	if c.Gemini.RateLimitPerMin <= 0 {
		return fmt.Errorf("GEMINI_RATE_LIMIT_PER_MIN must be positive")
	}
	if c.Worker.JobQueueSize <= 0 {
		return fmt.Errorf("WORKER_JOB_QUEUE_SIZE must be positive")
	}
	if c.Worker.TweetQueueSize <= 0 {
		return fmt.Errorf("WORKER_TWEET_QUEUE_SIZE must be positive")
	}
	return nil
}
