# ADR-005: Normalize providers behind a small interface

Status: accepted.

Providers implement normal completion, streaming, passive health, normalized
usage, and normalized errors. Routing owns policy and resilience. This supports a
credential-free fake, Gemini, and OpenAI-compatible APIs without leaking SDK
types into gateway or worker behavior.
