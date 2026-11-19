# Sanitized portal fixtures

Derived from the October 31 HAR's initial bootstrap and four RPC responses.
The fixtures preserve semantic UI structure, counters, node relationships, and
change ordering. Credentials and case field values are synthetic. Session keys,
nonessential text, attributes, and scalar list contents are replaced. Executable
JavaScript, constants, and push identifiers are omitted.

The bootstrap retains the English labels needed for component discovery.
The case stage in the final reply is "Synthetic stage". These files are for
offline testing only; they must never supply live session credentials.
