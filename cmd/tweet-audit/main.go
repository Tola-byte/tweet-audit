package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	httpSwagger "github.com/swaggo/http-swagger"

	_ "tweet-audit/docs"
	"tweet-audit/internal/config"
	"tweet-audit/internal/tweets/handler"
	"tweet-audit/internal/tweets/logger"
	"tweet-audit/internal/tweets/middleware"
	"tweet-audit/internal/tweets/model"
	"tweet-audit/internal/tweets/repository"
	"tweet-audit/internal/tweets/scorer"
	"tweet-audit/internal/tweets/service"
	"tweet-audit/internal/tweets/storage"
	"tweet-audit/internal/tweets/worker"
)

func main() {
	logger.Info("Starting Tweet Audit Server")

	if err := godotenv.Load(); err != nil {
		logger.Debug(".env file not found, using environment variables")
	}

	cfg, err := config.Load()
	if err != nil {
		logger.Error("Failed to load config: %v", err)
		log.Fatalf("Failed to load config: %v", err)
	}

	if err := cfg.Validate(); err != nil {
		logger.Error("Invalid config: %v", err)
		log.Fatalf("Invalid config: %v", err)
	}

	logger.Info("Initializing SQLite database")
	repo, err := repository.NewRepository(cfg.Database.Path, cfg.Database.BusyTimeout)
	if err != nil {
		logger.Error("Failed to initialize repository: %v", err)
		log.Fatalf("Failed to initialize repository: %v", err)
	}
	defer repo.Close()
	logger.Info("Database initialized at %s", cfg.Database.Path)

	svc := service.NewService(repo)

	logger.Info("Initializing file storage")
	fs := storage.NewLocalFileStore(cfg.Storage.UploadDir)
	logger.Info("File storage initialized at %s", cfg.Storage.UploadDir)

	var tweetScorer model.Scorer
	if cfg.Gemini.APIKey != "" {
		logger.Info("Initializing Gemini scorer")
		geminiScorer, err := scorer.NewGeminiScorerWithConfig(cfg.Gemini)
		if err != nil {
			logger.Warn("Failed to initialize Gemini scorer: %v, falling back to mock scorer", err)
			tweetScorer = scorer.NewDeterministicMockScorer()
		} else {
			tweetScorer = geminiScorer
			logger.Info("Gemini scorer initialized (model: %s, rate limit: %d/min)", cfg.Gemini.Model, cfg.Gemini.RateLimitPerMin)
		}
	} else {
		logger.Info("GEMINI_API_KEY not found, using mock scorer")
		tweetScorer = scorer.NewDeterministicMockScorer()
		logger.Info("Mock scorer initialized")
	}

	logger.Info("Starting worker")
	w := worker.NewWorkerWithConfig(fs, repo, tweetScorer, cfg.Worker)
	logger.Info("Worker started")

	logger.Info("Registering HTTP handlers")
	h := handler.NewHandlerWithConfig(svc, fs, w, cfg.Storage.MaxUploadSize)
	mux := http.NewServeMux()
	h.Register(mux)
	mux.Handle("/swagger/", httpSwagger.WrapHandler)

	validationMw := middleware.NewValidationMiddleware(middleware.ValidationConfig{
		MaxUploadSize: cfg.Storage.MaxUploadSize,
	})

	handler := validationMw(mux)

	server := &http.Server{
		Addr:         ":" + cfg.Server.Port,
		Handler:      handler,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	go func() {
		logger.Info("Server listening on :%s", cfg.Server.Port)
		logger.Info("Swagger UI available at http://localhost:%s/swagger/", cfg.Server.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server error: %v", err)
			log.Fatal(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("Server forced to shutdown: %v", err)
	}

	logger.Info("Shutting down worker...")
	workerCtx, workerCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer workerCancel()

	if err := w.Shutdown(workerCtx); err != nil {
		logger.Error("Worker shutdown error: %v", err)
	}

	logger.Info("Server stopped")
}
