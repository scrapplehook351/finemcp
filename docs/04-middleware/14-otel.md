---
url: "/docs/middleware/otel/"
title: "OpenTelemetry"
description: "Distributed tracing and metrics via OpenTelemetry"
weight: 15
---

The OTel middleware adds OpenTelemetry distributed tracing and metrics to all tool invocations.

## Usage

Zero-config setup using global providers:

```go
s.Use(middleware.OTel())
```

## Custom Providers

```go
s.Use(middleware.OTel(
    middleware.WithTracerProvider(tracerProvider),
    middleware.WithMeterProvider(meterProvider),
))
```

## Options

| Option | Description |
|--------|-------------|
| `WithTracerProvider(tp)` | Custom `trace.TracerProvider` |
| `WithMeterProvider(mp)` | Custom `metric.MeterProvider` |
| `WithTracing(bool)` | Enable/disable tracing |
| `WithMetrics(bool)` | Enable/disable metrics |

## What's Traced

Each tool invocation creates a span with:

- Tool name
- Duration
- Success/failure status
- Error details (if failed)

Metrics include:

- Tool invocation count
- Duration histogram
- Error rate
