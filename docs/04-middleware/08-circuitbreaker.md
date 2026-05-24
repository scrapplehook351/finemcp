---
url: "/docs/middleware/circuitbreaker/"
title: "Circuit Breaker"
description: "Fail fast after repeated failures to prevent cascading issues"
weight: 6
---

The circuit breaker middleware tracks failures and opens the circuit after a threshold, fast-failing subsequent calls until the service recovers.

## Usage

```go
s.Use(middleware.CircuitBreaker(
    middleware.WithFailureThreshold(5),
    middleware.WithSuccessThreshold(3),
))
```

## States

| State | Description |
|-------|-------------|
| **Closed** | Normal operation. Failures are counted. |
| **Open** | Circuit tripped. All calls fail immediately with `ErrCircuitOpen`. |
| **Half-Open** | Testing recovery. Limited calls allowed through. |

```
Closed → (failures >= threshold) → Open → (timeout expires) → Half-Open → (successes >= threshold) → Closed
                                                                        → (failure) → Open
```

## Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithFailureThreshold(n)` | 5 | Failures to open the circuit |
| `WithSuccessThreshold(n)` | 3 | Successes in half-open to close |
| `WithResetTimeout(d)` | — | Time to wait before half-open |
| `WithMaxHalfOpen(n)` | — | Max concurrent half-open attempts |
| `WithCircuitBreakerKeyFunc(fn)` | per-tool | Custom key for circuit isolation |
| `WithIsFailure(fn)` | all errors | Custom failure detection |
| `WithOnStateChange(fn)` | — | Callback on state transitions |

## State Change Callback

```go
s.Use(middleware.CircuitBreaker(
    middleware.WithOnStateChange(func(key string, from, to middleware.CircuitState) {
        log.Printf("Circuit %s: %s → %s", key, from, to)
    }),
))
```

## Error

When the circuit is open:

```go
var middleware.ErrCircuitOpen // "circuit breaker is open"
```
