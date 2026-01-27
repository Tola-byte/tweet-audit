# HERE FOR THIS, I'd be making use of async processing because doing synchronous can cause blocking and timeouts, some archives may be way too large and this will keep the HTTP open which can lead to timeout. 

# so best bet we have to use async with batch processing. 

# Concise ordered steps to build tweet-audit (practical sequence)

# Architecture: decide sync vs async upload processing, storage backend (local vs S3), and auth.
# Upload endpoint + filestore: accept X archive, store it, return StoredFile JSON (id, size, path/url).
# Parser: unzip / parse X archive formats; extract tweet id, text, created_at, links, attachments.
# Normalize: convert parsed items into TweetRecord domain objects.
# Deterministic filters: implement cheap rule-based checks (keywords, profanity lists, age checks) to pre-filter.
# Scorer abstraction: add Scorer interface so you can swap/mock LLM. Start with a no-op/mock scorer.
# Gemini integration: implement an adapter that batches prompts, handles rate limits, uses templates, and returns scores/labels.
# Orchestration pipeline: for each TweetRecord run filters → if uncertain call Scorer → compute final flag + reason.
# Persist results: store per-tweet score/flags and link to original archive/file.
# Review UI / export: list flagged tweets, show context, allow export of tweet URLs for manual deletion.
# Background jobs & reliability: move heavy parsing/LLM work to worker queue, add timeouts/retries.
# Security & privacy: auth, encryption, log scrubbing, retention policy, consent.
# Tests, monitoring, and cost controls: unit tests, integration tests with mocked Gemini, metrics on LLM calls/cost.

# TRADEOFFS

# Concise pros, cons, and mitigations

# Async processing

# Pros: decouples upload from heavy work (parsing + LLM), smoother UX, easy to retry/backoff.
# Cons: more infra (queue/workers), complexity in idempotency and status tracking.
# Mitigations: start with a simple local queue (channel + worker goroutine), persist job state, add idempotency keys.
# Local storage

# Pros: simplest to implement, fast for dev, no external creds/costs.
# Cons: single-node, not durable across deployments, no CDN, limited scaling, backup responsibility.
# Mitigations: store under a configurable data/uploads dir, write metadata to a small DB/file, add periodic backups, design FileStore interface so you can swap in S3 later.
# No auth (MVP)

# Pros: fastest iteration.
# Cons: major privacy & security risk — archives contain personal data; anyone hitting the endpoint could upload or download data.
# Mitigations: restrict to localhost or internal network while iterating; add simple API key or basic auth before any public testing; document the plan to add real auth before wider use.

# Archive file retention: delete immediately after processing

# Pros: minimal storage footprint, reduces privacy risk (no archives sitting on disk), forces you to rely on DB for data.
# Cons: can't reprocess archives if parsing logic changes, harder to debug failed jobs (no source file), no audit trail of original upload.
# Mitigations: all tweet data persisted to DB before deletion, job metadata kept for debugging, can re-upload if needed. If reprocessing needed, user can re-upload archive.

# Database choice: SQLite (current) vs MongoDB vs PostgreSQL

# SQLite (current implementation)
# Pros: zero config, file-based, perfect for MVP, easy backups (just copy file), no server needed.
# Cons: write contention (single writer), doesn't scale beyond single process, limited concurrent writes, not suitable for distributed workers.
# When to migrate: >100 concurrent jobs, need multiple worker processes, or distributed architecture.

# PostgreSQL alternative
# Pros: full ACID transactions, excellent for relationships (jobs → tweets → flagged), powerful SQL queries, mature and battle-tested, better for complex aggregations.
# Cons: requires server setup, more complex deployment, SQL learning curve, more rigid schema (migrations needed).
# Best for: when you need strong transactional guarantees, complex queries, or multi-worker setups.

# MongoDB alternative
# Pros: natural fit for document data (tweets are JSON-like), simpler code (no SQL, just Go structs), flexible schema (easy to evolve), can embed tweets in jobs, better for read-heavy workloads.
# Cons: limited multi-document transactions (or more complex), no joins (must embed or do application-level), less mature query language for complex aggregations.
# Best for: when you want simpler code, flexible schema evolution, or document-centric data model. Good fit for this use case since tweets are naturally documents.
# Tradeoff: if you need atomic saves across multiple documents (e.g., save all tweets + flagged tweets in one transaction), PostgreSQL is stronger. MongoDB supports multi-doc transactions but they're more limited.