# tweet-audit

This tool processes your X archive, evaluates each tweet against your alignment criteria (e.g., unprofessional language, specific keywords, outdated opinions), and generates a list of tweet URLs marked for manual deletion. This can be helpful to purging your X account with tweets that aren't social worthy or doesn't tell about your persona anymore.

## Architecture

The system follows a layered architecture with async processing:

**API Layer**: HTTP endpoints handle uploads, job status checks, and flagged tweet retrieval. Uploads return immediately with a job_id, avoiding long-running requests.

**Storage Layer**: Local file storage for archives and SQLite for job state, tweets, and flagged results. Both are designed for single-node operation with easy backup.

**Worker System**: Two background workers process jobs asynchronously:
- **Job Loop**: Parses archives, applies deterministic filters, enqueues tweets needing LLM review
- **Tweet Loop**: Processes tweets through Gemini API with rate limiting and circuit breaking

**LLM Scoring Layer**: Gemini API calls are wrapped in rate limiting (15 req/min), circuit breaker (fails fast on permanent errors), and exponential backoff retries. Falls back to mock scorer if no API key is configured.

**Data Flow**: Tweets flow from parser → filters → either flagged immediately or queued for Gemini → scored → persisted to database. All state is tracked in SQLite for querying and export.

## Processing Flow

1. **Upload**: Client uploads archive file via `POST /tweets/upload`. Server saves file, creates job record, enqueues job, returns `job_id` immediately.

2. **Archive Processing**: Job worker picks up job, opens archive file, parses ZIP or JS format, extracts tweet records, saves all tweets to database.

3. **Filtering**: For each tweet, deterministic filters check for retweets (skip), clear abuse patterns (flag immediately), or pass to LLM queue.

4. **LLM Scoring**: Tweet worker pulls from queue, waits for rate limiter token, checks circuit breaker, calls Gemini API with moderation criteria, receives score and reason.

5. **Persistence**: If flagged, saves flagged tweet record with score and reason. Marks tweet as processed, updates job statistics (processed_count, flagged_count, gemini_calls).

6. **Query**: Client polls `GET /jobs/:id` for status, then `GET /tweets?job_id=:id` to retrieve flagged tweets, or `GET /tweets/export` to get URLs for deletion.

## Two-Layer Detection System

```mermaid
graph LR
    subgraph "Layer 1: Deterministic Filters"
        A[Tweet Record] --> B{Retweet?}
        B -->|Yes| C[Skip]
        B -->|No| D{Abusive Patterns?}
        D -->|Yes| E[Flag Immediately]
        D -->|No| F{Threat Patterns?}
        F -->|Yes| E
        F -->|No| G{Pass to Layer 2}
    end

    subgraph "Layer 2: LLM Scoring"
        G --> H[Rate Limiter]
        H --> I[Circuit Breaker]
        I --> J[Gemini API]
        J --> K{Score > Threshold?}
        K -->|Yes| L[Flag with Reason]
        K -->|No| M[Safe Tweet]
    end

    E --> N[(Database)]
    L --> N
    M --> N

    style E fill:#ffcccc
    style L fill:#ffcccc
    style M fill:#ccffcc
    style C fill:#e0e0e0
```

## Resilience Patterns

```mermaid
graph TB
    subgraph "Rate Limiting"
        RL[Token Bucket<br/>15 tokens/min]
        RL -->|Token Available| Allow
        RL -->|No Token| Wait
    end

    subgraph "Circuit Breaker"
        CB[3 States:<br/>Closed/Open/Half-Open]
        CB -->|Success| Closed[Closed<br/>Normal Operation]
        CB -->|Failures > 5| Open[Open<br/>Fail Fast]
        CB -->|Timeout| HalfOpen[Half-Open<br/>Test Recovery]
        Open -->|30s timeout| HalfOpen
        HalfOpen -->|2 successes| Closed
        HalfOpen -->|Failure| Open
    end

    subgraph "Retry Logic"
        Retry[Exponential Backoff]
        Retry -->|429 Rate Limit| Wait60[Wait 60s]
        Retry -->|5xx Error| Backoff[Backoff: 1s, 2s, 4s]
        Retry -->|401/403/404| FailFast[Fail Fast<br/>No Retry]
    end

    Allow --> CB
    Wait --> RL
    Closed --> Retry
    Open --> FailFast
    HalfOpen --> Retry

    style Closed fill:#ccffcc
    style Open fill:#ffcccc
    style HalfOpen fill:#ffffcc
    style FailFast fill:#ffcccc
```

## Setup

### 1. Install Dependencies

```bash
go mod download
```

### 2. Setup Gemini API (Optional)

1. Get a Gemini API key from [Google AI Studio](https://makersuite.google.com/app/apikey)
2. Create a `.env` file:
   ```bash
   cp .env.example .env
   ```
3. Add your API key:
   ```
   GEMINI_API_KEY=your_actual_api_key_here
   ```

### 3. Run the Server

```bash
go run cmd/tweet-audit/main.go
```

The server will use Gemini if `GEMINI_API_KEY` is set, otherwise it falls back to the mock scorer.

## API Endpoints

- `POST /tweets/upload` - Upload archive (ZIP or JS file) with optional moderation criteria
- `GET /jobs/{id}` - Check job processing status and progress
- `GET /tweets?job_id={id}` - List flagged tweets with pagination
- `GET /tweets/export?job_id={id}&format=json|csv` - Export flagged tweet URLs
- `GET /swagger/` - Interactive API documentation

## Testing

Run all tests:

```bash
go test ./internal/tweets/...
```

Tests use `MockScorer` - no Gemini API calls, no costs, no network dependency. SQLite tests use temporary databases that are cleaned up automatically.

## Key Design Decisions

- **Async Processing**: Upload returns immediately with a job_id. Heavy work (parsing + LLM) happens in background workers
- **Two-Layer Detection**: Fast deterministic filters catch obvious cases, LLM handles nuanced ones
- **Rate Limiting**: Built-in 15 req/min limit for Gemini free tier compliance
- **Circuit Breaker**: Prevents cascading failures when API is down
- **SQLite**: Zero-config database perfect for single-user/small-team use
- **Local Storage**: Simple file-based storage, can swap to S3 later via FileStore interface

See [TRADEOFFS.md](./TRADEOFFS.md) for detailed architectural decisions and tradeoffs.
