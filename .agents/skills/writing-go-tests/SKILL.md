---
name: writing-go-tests
description: Write, restructure, or review Go tests using one suite per function, descriptive subtests, and data-driven cases for shared test logic. Use for Go test organization and readability; not for production-only Go changes.
---

# Writing Go Tests

Apply these conventions when writing or restructuring Go tests.

## Organize by function

- Give each function under test its own top-level suite, such as `TestEqual(test *testing.T)`. For methods, include the owning type when needed to distinguish subjects.
- Put distinct behaviors directly inside that suite as sequential `test.Run` calls. Name them in readable language: `"should continue when values match"` or `"should report failure when values differ"`. The suite supplies the subject; the subtest supplies the expected behavior and context.
- Keep each behavioral subtest focused on one outcome. Multiple checks can establish that outcome, such as verifying both a diagnostic and termination.
- Avoid suites that mix unrelated functions or cross-product tables of function callbacks and input variations.

## Choose direct subtests or data-driven cases

Use direct subtests when setup, invocation, or verification logic differs. Do not force different behaviors through a table of callbacks, boolean switches, or branches merely to eliminate repetition.

Use a table when cases share the same setup, invocation, and verification and differ only in data:

- Define readable, named fields such as `name`, `arguments`, and `expectedMessage`. Use keyed entries rather than positional rows.
- Prefer ranging directly over an inline `[]struct { ... }{ ... }` when the table has a single use. Use a named slice when that improves clarity or it is reused.
- Declare the table and loop directly in the function's suite. Do not add a `test.Run` wrapper whose only purpose is to launch the table's subtests.
- Give each generated subtest a descriptive behavior name that includes the scenario, so failure output remains understandable without the removed wrapper.
- Keep each case's state local to its subtest. Do not introduce parallel execution or additional nesting just for style.

A suite may contain both direct subtests and a table loop as siblings.

## Make the body readable

- Use descriptive identifiers: `test`, `scenario`, `recorder`, `expectedMessage`, `targetError`, and `arguments`. Avoid one-letter names and abbreviations such as `t`, `tc`, `tb`, or `args` in newly written or rewritten test code.
- In each behavioral subtest, separate setup, execution, and verification with `// Given`, `// When`, and `// Then`, once each, with blank lines between sections. Suite containers and table declarations do not need artificial sections or empty verification comments.
- Keep each section focused on its responsibility:
  - `Given` describes and establishes the initial state, including setup calls needed to seed that state.
  - `When` performs the complete behavior under test, including follow-up operations needed to observe its effects, and captures the results.
  - `Then` only verifies expectations against those results. Do not initiate further actions against the system under test there.
- Setup guards (such as checking that a test request was constructed successfully) can remain in `Given`. Arrange resource cleanup where needed; do not treat it as part of the behavior under test.
- Keep the invocation visible in the subtest. Extract helpers for repeated verification or meaningful test infrastructure, rather than hiding the behavior under a generic callback runner.
- Mark verification helpers with `test.Helper()`. Keep helpers and test doubles below the suites; retain a useful local internal-section divider.
- Follow repository receiver-naming conventions for methods on test doubles.

## Use reusable assertion helpers

- Use the repository's existing assertion helpers instead of repeating `if` checks followed by `test.Fatal` or `test.Fatalf`. Choose the helper that expresses the expectation directly, such as equality, deep equality, a true condition, absence of an error, or an error matching a target.
- Supply a concise reason when it clarifies the behavior being verified. Let the helper report expected and actual values rather than rebuilding that diagnostic in each test.
- Before adding an assertion helper, check whether an existing helper or a simple combination of helpers already expresses the expectation clearly.
- Add a helper only when it represents a generic, reusable expectation with uses beyond the current scenario. Give it a general contract and useful failure diagnostics, mark it with `test.Helper()`, and test its success and failure behavior. Avoid helpers named for an individual test case, hard-coded domain values, or a one-off workflow.
- Keep scenario-specific setup and verification in the test; do not turn each case into a new assertion API.
- When testing assertion helpers themselves, use independent checks where calling the helper under test would create circular verification. Direct `if` checks and fatal calls are appropriate for that exception.

## Preserve meaningful coverage

When restructuring, map existing behaviors to the new subtests before removing old tests. Preserve success, failure, boundary, and diagnostic checks. Test shared behavior thoroughly in one place, then verify that each caller integrates it correctly. Avoid repeating the entire shared-behavior matrix for every caller.

Reuse the repository's test doubles. Do not introduce a test framework just to obtain suites or parameterization; `testing.T.Run` and table loops provide both.

Format changed files with `gofmt`, run the owning package's tests, and follow applicable repository requirements for broader tests, race detection, and static checks. A skill-only change does not require Go test runs.
