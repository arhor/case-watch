---
name: writing-go-code
description: Write, refactor, or review Go code in the case-watch repository using its required receiver names and public-before-private file structure. Applies only to case-watch Go work.
---

# Writing Go Code

Apply these conventions in the case-watch repository. Read the applicable `AGENTS.md` for architecture and verification requirements. These are repository preferences, including where they differ from common Go style.

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
2. Public types.
3. Public methods and functions, including constructors.
4. Private (unexported) declarations: constants and variables, types, then methods and helper functions.

Keep all private top-level declarations after the public API, even when a public type or function references a private type declared later. Private fields remain inside their owning structs; they are not separate top-level declarations. Do not export an implementation detail merely to place it earlier in the file.

Use an explicit section divider before private declarations when the file contains both public and private declarations. Divider comment lines must be exactly 80 characters, including `//` and spaces. For example:

```go
// -- Internals ----------------------------------------------------------------
```

Use descriptive public section headings when they improve navigation, especially in files containing several related types. Keep those headings consistent and their divider lines 80 characters wide. Do not add empty sections.

`internal/utils/http_client.go` illustrates descriptive public sections and `this` receivers. `internal/services/case_service.go` and `internal/entrypoints/telegram/bot.go` illustrate the explicit internal implementation boundary. These are partial examples: existing files can mix types with methods, place private types early, or use other divider widths. Follow the ordering and width requirements above when writing or restructuring a non-test file.

The declaration-order and divider requirements do not apply to `*_test.go`; preserve useful test organization there.

## Applying changes

Make new and edited Go files conform to these conventions within the requested work. Do not turn an ordinary feature or fix into a repository-wide style rewrite unless requested. Preserve behavior when reorganizing declarations or renaming receivers.

Run `gofmt` on changed Go files. For behavior changes, add focused tests and follow the repository's normal, race-enabled, and static-check requirements in `AGENTS.md`. For a skill-only documentation change, validate the skill itself; Go test runs are unnecessary.
