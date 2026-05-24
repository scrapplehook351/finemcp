---
url: "/docs/features/content-types/"
title: "Content Types"
description: "Text, image, audio, and embedded resource responses"
weight: 8
---

MCP supports multiple content types in tool responses. finemcp provides constructors for each.

## Content Interface

All content types implement the sealed `Content` interface:

- `TextContent`
- `ImageContent`
- `AudioContent`
- `EmbeddedResource`

## TextContent

The default content type. Raw tool handlers (`[]byte` return) are automatically wrapped as text:

```go
tool, _ := finemcp.NewTool("hello",
    func(ctx context.Context, input []byte) ([]byte, error) {
        return []byte("Hello, World!"), nil
    },
)
```

Explicit construction:

```go
content := finemcp.TextContent{Text: "Hello, World!"}
```

## ImageContent

Binary image data with MIME type:

```go
content := finemcp.NewImageContent("image/png", pngBytes)
```

## AudioContent

Binary audio data with MIME type:

```go
content := finemcp.NewAudioContent("audio/wav", wavBytes)
```

## EmbeddedResource

Embed a resource content within a tool response:

```go
rc := finemcp.NewTextResourceContent("file:///data.json", `{"key": "value"}`)
content := finemcp.NewEmbeddedResource(rc)
```

## CallToolResult Constructors

For building complete tool results directly:

```go
finemcp.NewTextResult("success message")
finemcp.NewErrorResult("error message")
finemcp.NewImageResult("image/png", pngBytes)
finemcp.NewAudioResult("audio/wav", wavBytes)
finemcp.NewEmbeddedResourceResult(resourceContent)
```

## Streaming Mixed Content

Use the stream API to send different content types incrementally:

```go
stream := finemcp.StreamFromCtx(ctx)
if stream != nil {
    stream.SendText("Starting analysis...")
    stream.Send(finemcp.TextContent{Text: "Step 1 complete"})
    stream.Send(finemcp.NewImageContent("image/png", chartBytes))
}
```
