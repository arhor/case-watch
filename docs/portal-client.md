# Portal client

The production portal client is `internal/services/cases/portal.NewClient(portal.Options{})`. It retrieves a case's current stage through a bounded Vaadin RPC exchange.

Each check starts with a fresh cookie jar and a 60-second overall deadline. Individual requests have a 10-second timeout. The client makes at most six HTTP requests, spaces request starts by at least two seconds, and does not follow redirects or retry requests. Production connections are not reused, and request bodies cannot be replayed automatically. The client discovers current UI identifiers instead of reusing recorded session identifiers.

The adapter preserves the stage text returned by the portal and records completion time for successful observations. It reports authentication, transport, protocol, cancellation, and deadline failures through sanitized error categories; raw responses and credentials are not included in errors.

`portal.Options` supports injecting a base URL, single-exchange HTTP transport, clock, and cancellation-aware wait for offline verification. Production callers should use the default transport and timing dependencies. These options do not override the request or deadline budgets.

The reducer supports the portal's observed English-labeled Vaadin components and is not a general-purpose Vaadin implementation or a public portal API. Unexpected layouts fail instead of being interpreted as case status changes.

For historical implementation and validation records, see [implementation progress](implementation-progress.md) and [the independent audit](implementation-audit.md).
