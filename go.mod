// Isolated client module for the ab0t Audit Service (Product-1 event ingest).
// Stdlib-only: depends on NO server internals and no third-party packages, so it
// can be embedded in any mesh service's binary without dragging deps along.
module github.com/ab0t-com/audit-sdk-go

go 1.23
