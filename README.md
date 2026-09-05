# Comitia

Comitia is a coordination service for durable, delegated agent work. It provides one library-first Go application API for embedded use and for CLI, MCP, and HTTP adapters. Workspaces contain multiple work contexts that can reference issues, pull requests, documents, URLs, and other resources.

The project is under active development. The current tree is establishing the core API, SQLite persistence, and transport adapters. Interfaces and commands may change before the first release; command examples will be added after they are verified against the implemented adapters.

Comitia coordinates agents that run elsewhere. It does not execute agents or synchronize local files with remote resources.

See [Architecture](docs/architecture.md) for the domain and deployment design, and [Security model](docs/security.md) for trust boundaries and authorization invariants.
