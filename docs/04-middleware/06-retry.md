---
url: "/docs/middleware/retry/"
title: "Retry"
description: "Automatic retry with exponential backoff"
weight: 5
---

The retry middleware automatically retries failed tool invocations with configurable exponential backoff.

## Usage

```go
s.Use(middleware.Retry(
    middleware.WithMaxAttempts(3),
))
```

## Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithMaxAttempts(n)` | 3 | Maximum number of attempts |
| `WithBaseDelay(d)` | — | Initial delay between retries |
| `WithMaxDelay(d)` | — | Maximum delay cap |
| `WithMultiplier(m)` | — | Backoff multiplier |
| `WithJitter(frac)` | — | Random jitter fraction (0.0–1.0) |
| `WithRetryIsRetryable(fn)` | all errors | Custom function to determine if an error should be retried |
| `WithOnRetry(fn)` | — | Callback invoked on each retry |

## Example with Full Options

```go
s.Use(middleware.Retry(
    middleware.WithMaxAttempts(5),
    middleware.WithBaseDelay(100 * time.Millisecond),
    middleware.WithMaxDelay(5 * time.Second),
    middleware.WithMultiplier(2.0),
    middleware.WithJitter(0.1),
    middleware.WithRetryIsRetryable(func(err error) bool {
        // Only retry transient errors
        return strings.Contains(err.Error(), "timeout")
    }),
    middleware.WithOnRetry(func(ctx context.Context, attempt int, err error, delay time.Duration) {
        log.Printf("Retry attempt %d after %v: %v", attempt, delay, err)
    }),
))
```

## Retry Callback

```go
type RetryCallback func(ctx context.Context, attempt int, err error, delay time.Duration)
```

Useful for logging, metrics, or alerting on retries.
