package main

import (
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"
	httpSwagger "github.com/swaggo/http-swagger"

	_ "tweet-audit/docs"
	"tweet-audit/internal/tweets/handler"
	"tweet-audit/internal/tweets/logger"
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

	mux := http.NewServeMux()

	logger.Info("Initializing SQLite database")
	repo, err := repository.NewRepository("data/tweet-audit.db")
	if err != nil {
		logger.Error("Failed to initialize repository: %v", err)
		log.Fatalf("Failed to initialize repository: %v", err)
	}
	defer repo.Close()
	logger.Info("Database initialized")

	svc := service.NewService(repo)

	logger.Info("Initializing file storage")
	fs := storage.NewLocalFileStore("data/uploads")
	logger.Info("File storage initialized")

	// if Gemini API key not available, use mock scorer instead.
	var tweetScorer model.Scorer
	geminiAPIKey := os.Getenv("GEMINI_API_KEY")

	if geminiAPIKey != "" {
		logger.Info("Initializing Gemini scorer")
		geminiScorer, err := scorer.NewGeminiScorer(geminiAPIKey)
		if err != nil {
			logger.Warn("Failed to initialize Gemini scorer: %v, falling back to mock scorer", err)
			tweetScorer = scorer.NewDeterministicMockScorer()
		} else {
			tweetScorer = geminiScorer
			logger.Info("Gemini scorer initialized")
		}
	} else {
		logger.Info("GEMINI_API_KEY not found, using mock scorer")
		logger.Info("To use Gemini, set GEMINI_API_KEY in .env file")
		tweetScorer = scorer.NewDeterministicMockScorer()
		logger.Info("Mock scorer initialized")
	}

	logger.Info("Starting worker")
	w := worker.NewWorker(fs, repo, tweetScorer)
	logger.Info("Worker started")

	logger.Info("Registering HTTP handlers")
	h := handler.NewHandler(svc, fs, w)
	h.Register(mux)
	mux.Handle("/swagger/", httpSwagger.WrapHandler)
	logger.Info("Handlers registered")

	logger.Info("Server listening on :8080")
	logger.Info("Swagger UI available at http://localhost:8080/swagger/")
	log.Println("Starting server on :8080")
	err = http.ListenAndServe(":8080", mux)
	logger.Error("Server error: %v", err)
	log.Fatal(err)
}
