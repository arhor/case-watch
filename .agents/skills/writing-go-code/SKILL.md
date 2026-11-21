---
name: writing-go-code
description: Write, refactor, or review Go code in the case-watch repository using its required receiver names and public-before-private file structure. Applies only to case-watch Go work.
---

# Writing Go Code

Apply these conventions in the case-watch repository. Read the applicable `AGENTS.md` for architecture and verification requirements. These are repository preferences, including where they differ from common Go style.

## Import order

Group imports in this order, with one blank line between groups and empty groups omitted:

1. Standard library imports.
2. Third-party library imports.
3. Project imports from the case-watch module.

Apply this order to both production and test Go files.

## Method receivers

Name every named method receiver `this`, for pointer and value receivers alike. Never use single-letter receivers or type-derived receiver names. Update references in the method body when renaming a receiver. This rule also applies to methods in test files.

```go
func (this *Service) Close() {
    this.cancel()
}
```

## Non-test Go file structure

After the package declaration and imports, every non-test `.go` file MUST place declarations in this order, omitting empty groups:

1. Public (exported) constants, then exported package variables such as sentinel errors.
2. Public types, each preceded by its compile-time interface checks (if any) and followed by its exported constructors and public methods for that receiver type before the next type declaration.
3. Other public functions.
4. Private (unexported) declarations: constants and variables, then types, then private constructors, methods and helper functions.

Keep public methods together directly below their receiver type declaration, whether the receiver is a pointer or value. Exported constructors belong between the type declaration and its public methods. Except for compile-time interface checks, keep all private top-level declarations, including private constructors and methods, after the public API near the file end; private methods need not be adjacent to their receiver type declaration. This applies even when a public type or function references a private type declared later. Private fields remain inside their owning structs; they are not separate top-level declarations. Do not export an implementation detail merely to place it earlier in the file.

Place blank-identifier compile-time interface checks directly before the type declaration implementing those interfaces, in the same implementation file. Keep any type documentation comment attached to the type, between the checks and the declaration. Group multiple checks for the same type together; do not aggregate checks for different implementations in one file. These checks are an explicit exception to the private-declarations-at-the-end rule and do not require an `Internals` section by themselves.

```go
var _ models.Store = (*Store)(nil)

// Store implements transactional persistence.
type Store struct {
    // ...
}
```

Use an explicit section divider before private declarations when the file contains both public and private declarations. Divider comment lines must be exactly 80 characters, including `//` and spaces. For example:

```go
// -- Internals ----------------------------------------------------------------
```

Descriptive section headers other than `Internals` are allowed but optional, such as the `// -- HTTP Client ...` headers in `internal/platform/httpclient/http_client.go`. Preserve existing descriptive headers; they are not violations of the declaration-order rules. When used, keep their style consistent and their divider lines 80 characters wide. Do not add empty sections. The `Internals` divider remains required when the file contains both public and private declarations.

`internal/platform/httpclient/http_client.go` illustrates descriptive public sections and `this` receivers. `internal/services/tracked_case_service.go` and `internal/adapters/inbound/telegram/bot.go` illustrate the explicit internal implementation boundary. These are partial examples: existing files can separate public methods from their receiver types, place private types early, or use other divider widths. Follow the ordering and width requirements above when writing or restructuring a non-test file.

The declaration-order and divider requirements do not apply to `*_test.go`; preserve useful test organization there.

## Applying changes

Make new and edited Go files conform to these conventions within the requested work. Do not turn an ordinary feature or fix into a repository-wide style rewrite unless requested. Preserve behavior when reorganizing declarations or renaming receivers.

Run `gofmt` on changed Go files. For behavior changes, add focused tests and follow the repository's normal, race-enabled, and static-check requirements in `AGENTS.md`. For a skill-only documentation change, validate the skill itself; Go test runs are unnecessary.
