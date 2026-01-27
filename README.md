# Tweet Audit

A tool to audit your Twitter/X archive and identify potentially problematic tweets.

## Features

- Upload Twitter/X archives (ZIP or unzipped tweet.js files)
- Automatic detection of abusive, threatening, or harmful content
- Two-layer filtering: fast deterministic filters + LLM-based scoring
- Export flagged tweets for review/deletion
- RESTful API with Swagger documentation

## Architecture

### Two-Layer Detection System

1. **Deterministic Filters** (Fast, No Cost)
   - Pattern matching for clear-cut abuse
   - Catches: explicit threats, severe profanity, obvious hate speech
   - Runs first to reduce LLM API calls

2. **LLM Scorer** (Accurate, Context-Aware)
   - Evaluates nuanced cases that filters miss
   - Understands context, sarcasm, intent
   - Only processes tweets that pass filters (cost optimization)

## Setup

### 1. Install Dependencies

```bash
go mod download
```

### 2. Setup Gemini API (Optional but Recommended)

1. Get a Gemini API key from [Google AI Studio](https://makersuite.google.com/app/apikey)
2. Create a `.env` file in the project root:
   ```bash
   cp .env.example .env
   ```
3. Add your API key to `.env`:
   ```
   GEMINI_API_KEY=your_actual_api_key_here
   ```

### 3. Run the Server

```bash
go run cmd/tweet-audit/main.go
```

**That's it!** The server will:
- Use Gemini if `GEMINI_API_KEY` is set in `.env`
- Fall back to mock scorer if no API key is found

## API Endpoints

- `POST /tweets/upload` - Upload archive
- `GET /jobs/{id}` - Check job status
- `GET /tweets?job_id={id}` - List flagged tweets
- `GET /tweets/export?job_id={id}` - Export URLs
- `GET /swagger/` - API documentation

## Why Use an LLM?

### Pattern Matching Limitations:
- Can't understand context ("I hate Mondays" vs actual hate)
- Misses subtle abuse and coded language
- False positives on jokes/sarcasm
- Can't detect intent

### LLM Advantages:
- Understands context and nuance
- Detects subtle forms of abuse
- Handles sarcasm and cultural context
- Explains why it flagged something
- Much lower false positive rate

## Cost Optimization

The system is designed to minimize LLM API costs:

1. **Deterministic filters run first** - catch obvious cases (no API call)
2. **Only uncertain tweets go to LLM** - saves ~80-90% of API calls
3. **Batch processing** - can optimize further with batch API calls

For a 10,000 tweet archive:
- ~1,000-2,000 might need LLM evaluation
- ~8,000-9,000 caught by filters (no cost)
- Estimated cost: $0.50-$2.00 (depending on provider)
