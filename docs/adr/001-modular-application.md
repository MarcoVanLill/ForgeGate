# ADR-001: Start as a modular application

Status: accepted.

ForgeGate builds two processes from one module. Package boundaries isolate HTTP,
jobs, storage, routing, and providers without remote calls between them. This
minimizes deployment and consistency complexity while preserving extraction
points. Split a component only when measured scaling, ownership, or isolation
needs justify the operational cost.
