-- ADR 0018: keep tracking data; discard obsolete automatic delivery state.
DROP TRIGGER pending_recipient;
DROP TABLE pending;
DROP TABLE recipients;
