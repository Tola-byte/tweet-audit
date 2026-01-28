## Architecture choices

Shape is simple on purpose: HTTP API → filestore → worker + SQLite → Gemini scorer.

We went for a background-worker setup instead of “do everything in the request” because archives can be big and LLM calls are slow and ratelimited. Keeping the upload request short (store file + create job + return job_id) avoids timeouts and makes the UX predictable. SQLite + local disk are enough for a single-user / small-team tool and keep ops basically at “copy a folder and a binary”.

Go is a good fit here: standard HTTP server, sane concurrency primitives, and a static binary that runs anywhere without dragging a runtime around.

## Concurrency strategy

Uploads are synchronous only up to “file saved and job created”. After that:
- `jobLoop` parses the archive, normalizes tweets, filters, and enqueues Gemini work.
- `tweetLoop` pulls from a queue and talks to Gemini under rate limiting and a circuit breaker.

So conceptually: sequential upload, async batch processing. It gives us clear places to cap throughput (queue sizes, rate limiter) without exposing that complexity to the client.

## Error handling

For the DB we lean on SQLite WAL + busy_timeout + a small retry wrapper for `SQLITE_BUSY` so “database is locked” turns into a short backoff instead of a crash. For Gemini we do:
- rate limiter in front
- circuit breaker around the calls
- small, bounded retry with backoff for transient errors

Permanent problems (bad key, wrong model, 4xx that are not 429) trip the breaker quickly and we stop wasting calls. Jobs can end in a “failed” state but their partial data stays in the DB so you can still inspect what happened.

## Performance vs safety

We bias toward “safe and understandable” over raw speed:
- Retweets are dropped early to avoid wasting quota.
- Deterministic filters are conservative; when in doubt we let Gemini decide.
- Daily quota and per-minute caps are enforced in code and persisted so a restart does not hammer the API.

SQLite + local files are a conscious “single-node” tradeoff. For this use case (your own archive, maybe a few jobs) that is fine. If you ever need more concurrency or multiple workers, the same schema and code can move to PostgreSQL without a rewrite.