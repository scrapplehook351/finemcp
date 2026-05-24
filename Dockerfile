# Dockerfile for GoReleaser - uses pre-built binary
# Produces a minimal container (~10MB) with just the binary

FROM alpine:latest AS certs
RUN apk add --no-cache ca-certificates tzdata

# Final stage - minimal runtime image
FROM scratch

# Import certificates and timezone data
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=certs /usr/share/zoneinfo /usr/share/zoneinfo

# Copy the pre-built binary from GoReleaser
COPY finemcp /usr/local/bin/finemcp

# Default command
ENTRYPOINT ["/usr/local/bin/finemcp"]
CMD ["--help"]

# Metadata
LABEL org.opencontainers.image.title="FineMCP"
LABEL org.opencontainers.image.description="Production-grade MCP (Model Context Protocol) framework for Go"
LABEL org.opencontainers.image.url="https://github.com/finemcp/finemcp"
LABEL org.opencontainers.image.documentation="https://github.com/finemcp/finemcp/blob/main/README.md"
LABEL org.opencontainers.image.source="https://github.com/finemcp/finemcp"
LABEL org.opencontainers.image.licenses="MIT"
