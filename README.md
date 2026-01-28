# tweet-audit

This tool processes your X archive, evaluates each tweet against your alignment criteria (e.g., unprofessional language, specific keywords, outdated opinions), and generates a list of tweet URLs marked for manual deletion. This can be helpful to purging your X account with tweets that aren't social worthy or doesn't tell about your persona anymore.

## Architecture

```mermaid
graph TB
    subgraph "API Layer"
        API[HTTP API Server]
        Upload[POST /tweets/upload]
        Status[GET /jobs/:id]
        List[GET /tweets?job_id=:id]
        Export[GET /tweets/export?job_id=:id]
    end

    subgraph "Storage Layer"
        FileStore[Local FileStore]
        DB[(SQLite Database)]
    end

    subgraph "Worker System"
        JobQueue[Job Queue<br/>chan string]
        TweetQueue[Tweet Queue<br/>chan QueuedTweet]
        
        JobLoop[Job Loop Worker]
        TweetLoop[Tweet Loop Worker]
        
        Parser[Archive Parser]
        Filters[Deterministic Filters]
    end

    subgraph "LLM Scoring Layer"
        RateLimiter[Rate Limiter<br/>15 req/min]
        CircuitBreaker[Circuit Breaker]
        Retry[Exponential Backoff]
        Gemini[Gemini API<br/>gemini-2.5-flash-lite]
        MockScorer[Mock Scorer<br/>fallback]
    end

    subgraph "Data Flow"
        direction TB
        Tweets[Tweet Records]
        Flagged[Flagged Tweets]
        Jobs[Job Status]
    end

    API --> Upload
    API --> Status
    API --> List
    API --> Export

    Upload --> FileStore
    Upload --> JobQueue
    
    FileStore --> Parser
    JobQueue --> JobLoop
    
    JobLoop --> Parser
    Parser --> Tweets
    Tweets --> Filters
    
    Filters -->|Clear Abuse| Flagged
    Filters -->|Needs Review| TweetQueue
    
    TweetQueue --> TweetLoop
    TweetLoop --> RateLimiter
    RateLimiter --> CircuitBreaker
    CircuitBreaker --> Retry
    Retry --> Gemini
    Retry --> MockScorer
    
    Gemini -->|Score Result| TweetLoop
    MockScorer -->|Score Result| TweetLoop
    
    TweetLoop -->|Flagged| Flagged
    TweetLoop -->|Processed| DB
    
    JobLoop --> DB
    TweetLoop --> DB
    Status --> DB
    List --> DB
    Export --> DB
    
    DB --> Jobs
    DB --> Tweets
    DB --> Flagged

    style API fill:#e1f5ff
    style JobQueue fill:#fff4e1
    style TweetQueue fill:#fff4e1
    style Gemini fill:#ffe1f5
    style DB fill:#e1ffe1
    style Flagged fill:#ffe1e1
```

## Processing Flow

```mermaid
sequenceDiagram
    participant Client
    participant API
    participant FileStore
    participant JobQueue
    participant JobLoop
    participant Parser
    participant Filters
    participant TweetQueue
    participant TweetLoop
    participant RateLimiter
    participant Gemini
    participant DB

    Client->>API: POST /tweets/upload<br/>(archive + criteria)
    API->>FileStore: Save archive file
    FileStore-->>API: file_id
    API->>JobQueue: Enqueue job(file_id, criteria)
    API-->>Client: 201 {job_id, file_id}
    
    JobQueue->>JobLoop: Process job
    JobLoop->>FileStore: Open archive
    FileStore-->>JobLoop: Archive data
    JobLoop->>Parser: Parse archive
    Parser-->>JobLoop: Tweet records
    
    loop For each tweet
        JobLoop->>Filters: Check deterministic rules
        alt Clear abuse detected
            Filters-->>JobLoop: Flag immediately
            JobLoop->>DB: Save flagged tweet
        else Needs LLM review
            JobLoop->>TweetQueue: Enqueue for Gemini
        end
    end
    
    TweetQueue->>TweetLoop: Process tweet
    TweetLoop->>RateLimiter: Wait for quota
    RateLimiter-->>TweetLoop: Proceed
    TweetLoop->>Gemini: Score tweet (with criteria)
    Gemini-->>TweetLoop: Score result
    
    alt Should flag
        TweetLoop->>DB: Save flagged tweet
    end
    
    TweetLoop->>DB: Mark tweet processed
    TweetLoop->>DB: Update job stats
    
    Client->>API: GET /jobs/:id
    API->>DB: Query job status
    DB-->>API: Job with progress
    API-->>Client: Job status + metrics
    
    Client->>API: GET /tweets?job_id=:id
    API->>DB: Query flagged tweets
    DB-->>API: Flagged tweets list
    API-->>Client: Paginated results
```

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
