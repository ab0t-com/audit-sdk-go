package auditclient

// Version is the SDK's released version. It is reported in the User-Agent of
// every ingest request so the audit service can attribute traffic and spot
// clients running a version with a known-bad contract.
//
// Keep this in step with the git tag: a tag of v0.1.0 means Version == "0.1.0".
const Version = "0.1.0"
