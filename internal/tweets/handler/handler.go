package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"tweet-audit/internal/tweets/logger"
	"tweet-audit/internal/tweets/model"
	"tweet-audit/internal/tweets/service"
	"tweet-audit/internal/tweets/util"
	"tweet-audit/internal/tweets/worker"
)

type Handler struct {
	svc           *service.Service
	fileStore     model.FileStore
	worker        *worker.Worker
	maxUploadSize int64
}

func NewHandler(svc *service.Service, fs model.FileStore, w *worker.Worker) *Handler {
	return &Handler{svc: svc, fileStore: fs, worker: w, maxUploadSize: 0}
}

func NewHandlerWithConfig(svc *service.Service, fs model.FileStore, w *worker.Worker, maxUploadSize int64) *Handler {
	return &Handler{svc: svc, fileStore: fs, worker: w, maxUploadSize: maxUploadSize}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/tweets", util.MethodHandler(http.MethodGet)(h.List))
	mux.HandleFunc("/tweets/view", util.MethodHandler(http.MethodGet)(h.View))
	mux.HandleFunc("/tweets/export", util.MethodHandler(http.MethodGet)(h.Export))
	mux.HandleFunc("/jobs/", util.MethodHandler(http.MethodGet)(h.JobStatus))
	mux.HandleFunc("/tweets/upload", util.MethodHandler(http.MethodPost)(h.Upload))
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	jobID := r.URL.Query().Get("job_id")
	page := util.ParseInt(r.URL.Query().Get("page"), 1)
	pageSize := util.ParseIntWithMax(r.URL.Query().Get("page_size"), 20, 100)

	logger.WithFields(map[string]interface{}{
		"job_id": jobID,
		"page":   page,
		"size":   pageSize,
	}).Info("Listing flagged tweets")

	tweets, total, err := h.svc.ListFlaggedTweets(jobID, page, pageSize)
	if err != nil {
		logger.Error("Failed to list tweets: %v", err)
		util.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list tweets: %v", err))
		return
	}

	logger.Info("Retrieved %d tweets (total: %d)", len(tweets), total)

	response := map[string]interface{}{
		"tweets": tweets,
		"pagination": map[string]interface{}{
			"page":        page,
			"page_size":   pageSize,
			"total":       total,
			"total_pages": (total + pageSize - 1) / pageSize,
		},
	}

	_ = util.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) View(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		util.WriteError(w, http.StatusBadRequest, "id parameter is required")
		return
	}

	tweet, err := h.svc.GetFlaggedTweet(id)
	if err != nil {
		util.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get tweet: %v", err))
		return
	}

	if tweet == nil {
		util.WriteError(w, http.StatusNotFound, "tweet not found")
		return
	}

	_ = util.WriteJSON(w, http.StatusOK, tweet)
}

func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	jobID := r.URL.Query().Get("job_id")
	format := util.GetQueryParam(r, "format", "json")

	urls, err := h.svc.ExportFlaggedTweets(jobID)
	if err != nil {
		util.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to export tweets: %v", err))
		return
	}

	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=flagged_tweets.csv")
		for _, url := range urls {
			_, _ = w.Write([]byte(url + "\n"))
		}
	} else {
		response := map[string]interface{}{
			"urls":   urls,
			"count":  len(urls),
			"job_id": jobID,
		}
		_ = util.WriteJSON(w, http.StatusOK, response)
	}
}

func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger.Info("Upload request received")

	maxUploadSize := h.maxUploadSize
	if maxUploadSize == 0 {
		maxUploadSize = int64(500 << 20) // Default 500MB
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		logger.Warn("Failed to parse multipart form: %v", err)
		util.WriteError(w, http.StatusBadRequest, "failed to parse multipart form: file too large or invalid")
		return
	}

	file, handler, err := r.FormFile("file")
	if err != nil {
		logger.Warn("File not found in request: %v", err)
		util.WriteError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	logger.Info("Saving file: %s (size: %d bytes)", handler.Filename, handler.Size)

	stored, err := h.fileStore.Save(ctx, handler.Filename, file)
	if err != nil {
		logger.Error("Failed to save file: %v", err)
		util.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}

	logger.Info("File saved with ID: %s", stored.ID)

	var criteria *model.ModerationCriteria
	criteriaStr := r.FormValue("criteria")
	if criteriaStr != "" {
		var criteriaObj struct {
			Criteria model.ModerationCriteria `json:"criteria"`
		}
		if err := json.Unmarshal([]byte(criteriaStr), &criteriaObj); err == nil {
			criteria = &criteriaObj.Criteria
		} else {
			var directCriteria model.ModerationCriteria
			if err := json.Unmarshal([]byte(criteriaStr), &directCriteria); err == nil {
				criteria = &directCriteria
			} else {
				logger.Warn("Failed to parse criteria JSON: %v, using defaults", err)
				criteria = nil
			}
		}
		if criteria != nil {
			logger.Info("Custom criteria provided: %d forbidden words, professional_check=%v, exclude_politics=%v",
				len(criteria.ForbiddenWords), criteria.ProfessionalCheck, criteria.ExcludePolitics)
		}
	}

	jobID := h.worker.EnqueueParse(stored.ID, criteria)
	logger.Info("Job queued: job_id=%s file_id=%s", jobID, stored.ID)

	_ = util.WriteJSON(w, http.StatusCreated, model.UploadResponse{File: stored, JobID: jobID})
}

func (h *Handler) JobStatus(w http.ResponseWriter, r *http.Request) {
	id := util.ExtractPathParam(r.URL.Path, "/jobs/")
	if id == "" {
		util.WriteError(w, http.StatusBadRequest, "job id required")
		return
	}
	job, ok := h.worker.GetJob(id)
	if !ok {
		util.WriteError(w, http.StatusNotFound, "job not found")
		return
	}
	_ = util.WriteJSON(w, http.StatusOK, job)
}
