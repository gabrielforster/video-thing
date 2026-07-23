# ADR-0001: Go for Backend Services

## Status
Accepted

## Context
The platform needs two backend services with very different runtime profiles that nonetheless share a codebase, deployment pipeline, and team:

- An **API service** handling upload orchestration, metadata CRUD, auth, and signed-URL issuance — latency-sensitive, high concurrency, mostly I/O-bound.
- A **worker service** that polls SQS, pulls source video from S3, shells out to/orchestrates ffmpeg for transcoding into multi-bitrate HLS, and writes results back to S3 — a mix of CPU-bound (encode supervision, manifest generation) and I/O-bound (S3/SQS calls) work that must scale elastically with ECS Fargate based on queue depth.

For an MVP built and operated by a small team, the language choice has to optimize for: fast iteration speed, low operational surface area (container size, startup time, dependency management), predictable concurrency behavior under bursty load, and low ramp-up cost so any engineer can work on either service. The frontend is a separate concern (browser + hls.js) and does not force a "one language everywhere" constraint on the backend.

## Decision
Use **Go** for both the API service and the worker service, with **Gin** as the HTTP framework, **sqlc** for typed SQL access to Postgres, and **pgx** as the underlying driver.

## Alternatives Considered

- **Node.js / TypeScript** — Appealing for a single-language stack with a JS/TS frontend, and the ecosystem has mature S3/SQS SDKs. Rejected because the worker's job is fundamentally about supervising OS processes (ffmpeg) and managing many concurrent long-running I/O operations under backpressure from a queue; Node's single-threaded event loop makes CPU-adjacent orchestration (progress parsing, concurrent multi-rendition supervision) awkward without worker_threads or a second runtime concern. TypeScript's structural typing also requires additional tooling (zod, strict tsconfig, io-ts) to get the same compile-time guarantees Go provides by default, and that tooling is easy to skip under MVP deadline pressure, quietly reintroducing runtime type errors.

- **Python** — Has the richest video/ffmpeg tooling ecosystem (ffmpeg-python, PyAV, moviepy) and would be the fastest path to writing the actual transcoding logic. Rejected primarily because of the GIL: the worker needs true concurrent supervision of multiple ffmpeg subprocesses and concurrent S3/SQS I/O, and while asyncio or multiprocessing can work around the GIL, both add complexity that Go gives for free via goroutines. Python's packaging story (venvs, native dependency wheels, slower cold starts) also makes Fargate scale-out less predictable, and the API service's performance under concurrent load would be meaningfully worse than Go without dropping to something like a compiled extension or a separate ASGI tuning effort — not a good trade for an MVP.

- **Rust** — Would give the best raw performance, memory safety, and the tightest possible ffmpeg FFI integration. Rejected for MVP purposes because the ramp-up cost is too high relative to team size and timeline: borrow-checker friction slows iteration speed precisely when the team needs to move fast and change data models and API shapes frequently. Rust's benefits (zero-cost abstractions, no GC pauses) are not the bottleneck for this workload — the bottleneck is ffmpeg itself and network I/O, not the orchestration language — so the added development cost buys little at this stage.

## Consequences

### Positive
- Single static binary per service; Docker images can be minimal (`scratch`/`distroless`-based), which speeds up ECR pulls and Fargate task startup — directly helping the scale-from-zero worker pattern.
- Goroutines + channels map naturally onto "poll SQS, fan out N ffmpeg jobs, wait for completion" without an async runtime or callback complexity.
- Strong standard library (`net/http`, `context`, `os/exec`) covers most of what both services need without heavy third-party dependency trees, reducing supply-chain surface.
- sqlc gives compile-time-checked SQL access without a full ORM's runtime magic, keeping query behavior predictable and easy to review.
- One language for both services means engineers can move between the API and worker codebases freely, and CI/CD, linting, and base images are shared.

### Negative / Tradeoffs
- Go's error handling (explicit `if err != nil` propagation) is more verbose than exceptions and can obscure control flow in deeply nested orchestration code if not disciplined about wrapping errors with context.
- Smaller pool of ffmpeg-specific libraries than Python; the worker will shell out to the ffmpeg binary directly via `os/exec` rather than using a native binding, which means parsing stdout/stderr for progress rather than getting structured callbacks.
- Generics and the broader ecosystem (e.g., observability libraries) are less mature than in Node's or Python's ecosystems, occasionally requiring more glue code.
- Hiring/onboarding pool for Go is smaller than for Node or Python, though this is a minor concern given the team is already committed to it for both services.

## Notes
Revisit if the worker's transcoding logic needs to move from CLI-orchestrated ffmpeg to a more tightly integrated native pipeline (e.g., GPU-accelerated encoding via vendor SDKs that only ship C/C++ or Python bindings) — at that point a hybrid approach (Go orchestrator + a purpose-built encode microservice in another language) could make sense. Also revisit if the API service's needs diverge sharply from the worker's (e.g., needing a GraphQL layer with heavy ecosystem support), which might justify splitting languages per service rather than sharing one.
