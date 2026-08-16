package olympus

// Version is the one literal every door reports: the CLI verb, the MCP tool,
// and the MCP server's own identity. Sharing it is what stops two doors
// disagreeing about what is running.
//
// A var rather than a const so the release can stamp the tag into it. It was a
// const, and the release configuration injected nothing — which meant every
// published binary would have reported this development placeholder whatever
// version it actually was. api §7 makes this the literal a consumer
// floor-checks against, so a wrong answer here is not cosmetic: it breaks the
// one check a client has, and it puts the wrong version on every bug report.
//
// Treat it as read-only. It is a var for the linker's benefit, not the
// caller's.
var Version = "0.1.0-dev"
