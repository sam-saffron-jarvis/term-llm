# Context must be prepared from checksum-verified static release archives; see README.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
ARG TARGETARCH
COPY --chmod=0555 linux_${TARGETARCH}/term-llm /usr/local/bin/term-llm
USER 65532:65532
EXPOSE 8082
ENTRYPOINT ["/usr/local/bin/term-llm", "serve", "proxy"]
CMD ["--host", "0.0.0.0", "--port", "8082", "--state-dir", "/state"]
